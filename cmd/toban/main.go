// toban は Tracker の Issue を拾って、コーディングエージェントを起動するオーケストレータ。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/ynny-github/toban/internal/daemon"
	"github.com/ynny-github/toban/internal/executor/orca"
	"github.com/ynny-github/toban/internal/scaffold"
	"github.com/ynny-github/toban/internal/state"
	"github.com/ynny-github/toban/internal/tick"
	"github.com/ynny-github/toban/internal/tracker/github"
	"github.com/ynny-github/toban/internal/workflow"
)

// version はリリースのビルドで goreleaser が -ldflags で埋める。
var version = "dev"

// versionString は、ldflags がなければ go install が埋めたモジュールのバージョンを返す。
func versionString() string {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

const usage = `usage: toban [-config <dir>] [-state <dir>] <command>

設定のディレクトリ（config.yaml を置く。Tracker ごとの Workflow も config.yaml の trackers に書く）は必須。-config か環境変数 TOBAN_CONFIG で指定する。

state ディレクトリ（ロック、PID、ログ、継続の回数を覚える state.json を置く）の既定は $XDG_STATE_HOME/toban（なければ ~/.local/state/toban）。-state か環境変数 TOBAN_STATE で変える。

commands:
  init <dir>          <dir>（必須）に雛形（config.yaml）を作る
  tick                すべての Workflow を 1 回ずつ処理し、止まった Working の Issue に継続を送り、Candidate を Dispatch する
  run                 前面で tick を config.yaml の間隔で繰り返す（コンテナの起動コマンド向け）
  start               run を背景に切り離して起動する（ログは state ディレクトリの toban.log。回転する）
  stop                背景の run を止める（実行中の tick が終わってから止まる）
  status              背景の run が動いているかを表示する
  version             バージョンを表示する（-version でも同じ。設定のディレクトリは不要）
`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage); flag.PrintDefaults() }
	configDir := flag.String("config", os.Getenv("TOBAN_CONFIG"), "設定のディレクトリ（既定は環境変数 TOBAN_CONFIG）")
	stateOverride := flag.String("state", os.Getenv("TOBAN_STATE"), "state ディレクトリ（既定は環境変数 TOBAN_STATE）")
	showVersion := flag.Bool("version", false, "バージョンを表示して終了する")
	flag.Parse()
	if *showVersion || flag.Arg(0) == "version" {
		fmt.Println("toban", versionString())
		return
	}
	// init のディレクトリは引数で必ず指定する。ほかのコマンドは -config か TOBAN_CONFIG
	if flag.Arg(0) == "init" {
		if flag.NArg() != 2 {
			flag.Usage()
			os.Exit(2)
		}
		*configDir = flag.Arg(1)
	}
	if *configDir == "" {
		flag.Usage()
		os.Exit(2)
	}

	var err error
	switch flag.Arg(0) {
	case "tick":
		var dir string
		if dir, err = state.Dir(*stateOverride); err == nil {
			err = runTick(context.Background(), log, *configDir, dir)
		}
	case "init":
		if err = scaffold.Init(*configDir); err == nil {
			log.Info("created", "dir", *configDir, "next", "uncomment the trackers example in config.yaml and edit it, then run `toban start`")
		}
	case "run", "start", "stop", "status":
		var dir string
		if dir, err = state.Dir(*stateOverride); err == nil {
			err = runDaemonCommand(log, flag.Arg(0), *configDir, dir)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("failed", "error", err)
		os.Exit(1)
	}
}

func runTick(ctx context.Context, log *slog.Logger, configDir, stateDir string) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	release, ok, err := tick.Lock(filepath.Join(cache, "toban", "tick.lock"))
	if err != nil {
		return err
	}
	if !ok {
		log.Info("another tick is running; skipping")
		return nil
	}
	defer release()

	settings, err := workflow.LoadSettings(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		return err
	}
	// 確認できないときも見送る。見送っても Candidate は Ready のまま残り、次の tick でやり直す
	limits, err := orca.New(orca.Config{}, orca.Command("orca")).RateLimits(ctx)
	if err != nil {
		return fmt.Errorf("cannot check Claude usage; not dispatching: %w", err)
	}
	if why := limits.Over(settings.MaxSessionPercent, settings.MaxWeeklyPercent, 15*time.Minute, time.Now()); why != "" {
		log.Info("Claude usage is over the limit; not dispatching", "reason", why)
		return nil
	}

	if _, err := os.Stat(filepath.Join(configDir, "workflows")); err == nil {
		log.Warn("workflows/ is no longer read; move each Workflow into the trackers list in config.yaml")
	}
	// Workflow に誤りがあれば、どの Project も処理しない（誤った設定のまま一部だけ動かさない）
	workflows, err := workflow.LoadWorkflows(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		return err
	}
	var targets []tick.Target
	var errs []error
	for _, w := range workflows {
		t, err := target(w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		targets = append(targets, t)
	}
	hints, err := state.Load(stateDir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	c := tick.Continuation{After: settings.ContinueAfter, Max: settings.MaxContinuations, Now: time.Now(), Hints: hints, Partial: len(errs) > 0}
	if err := tick.Run(ctx, log, targets, c); err != nil {
		errs = append(errs, err)
	}
	if err := hints.Save(stateDir); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// target は Workflow の kind に応じて、組み込みの Tracker と Executor を組み立てる。
func target(w *workflow.Workflow) (tick.Target, error) {
	t := tick.Target{Name: w.Name, MaxConcurrent: w.MaxConcurrent, Render: w.Render}
	switch w.Tracker.Kind {
	case "github":
		tr, err := github.New(*w.Tracker.GitHub, github.Command("gh"))
		if err != nil {
			return t, fmt.Errorf("%s: %w", w.Name, err)
		}
		t.Tracker = tr
	}
	switch w.Executor.Kind {
	case "orca":
		t.Executor = orca.New(*w.Executor.Orca, orca.Command("orca"))
	}
	return t, nil
}

// validate は config.yaml が読めることを確かめる（run を始める前に、誤りをその場で知らせるため）。
func validate(configDir string) (workflow.Settings, error) {
	path := filepath.Join(configDir, "config.yaml")
	s, err := workflow.LoadSettings(path)
	if err != nil {
		return s, err
	}
	_, err = workflow.LoadWorkflows(path)
	return s, err
}

func runDaemonCommand(log *slog.Logger, command, configDir, stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	p := daemon.PathsIn(stateDir)
	switch command {
	case "run":
		return runLoop(log, configDir, stateDir, p)
	case "start":
		if _, err := validate(configDir); err != nil {
			return err
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if self, err = filepath.EvalSymlinks(self); err != nil {
			return err
		}
		if configDir, err = filepath.Abs(configDir); err != nil {
			return err
		}
		pid, err := daemon.Start(self, []string{"-config", configDir, "-state", stateDir, "run"}, p)
		if err != nil {
			return err
		}
		log.Info("started", "pid", pid, "log", p.Log)
	case "stop":
		stopped, err := daemon.Stop(p, 30*time.Second)
		if err != nil {
			return err
		}
		if stopped {
			log.Info("stopped")
		} else {
			log.Info("still finishing the current tick; it will stop afterwards")
		}
	case "status":
		pid, running, err := daemon.Status(p)
		if err != nil {
			return err
		}
		if running {
			log.Info("running", "pid", pid, "log", p.Log)
		} else {
			log.Info("not running")
		}
	}
	return nil
}

// runLoop は tick を config.yaml の間隔で繰り返す。設定は周ごとに読み直すので、間隔を変えても再起動は要らない。
// 始める前の設定の誤りは終了にするが、始めたあとの tick の失敗はログに出して次の周に進む。
func runLoop(log *slog.Logger, configDir, stateDir string, p daemon.Paths) error {
	settings, err := validate(configDir)
	if err != nil {
		return err
	}
	release, err := daemon.Acquire(p)
	if err != nil {
		return err
	}
	defer release()
	// start から起動されたときは TOBAN_LOG のファイルに、回転させながら書く
	if path := os.Getenv("TOBAN_LOG"); path != "" {
		// shortcut: 10 MiB × 3 世代で固定, 変えたくなったら config.yaml の設定にする
		log = slog.New(slog.NewTextHandler(&daemon.RotatingFile{Path: path, Max: 10 << 20, Keep: 3}, nil))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("running", "pid", os.Getpid(), "interval", settings.Interval)
	interval := settings.Interval
	daemon.Loop(ctx, func() time.Duration {
		if s, err := workflow.LoadSettings(filepath.Join(configDir, "config.yaml")); err != nil {
			log.Error("cannot reload config.yaml; keeping the previous interval", "error", err)
		} else {
			interval = s.Interval
		}
		return interval
	}, func() {
		// ctx は渡さない。止める合図が来ても、Dispatch の途中で止めず、この tick を終える
		if err := runTick(context.Background(), log, configDir, stateDir); err != nil {
			log.Error("tick failed", "error", err)
		}
	})
	log.Info("stopped")
	return nil
}
