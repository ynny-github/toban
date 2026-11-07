// toban は Tracker の Issue を拾って、コーディングエージェントを起動するオーケストレータ。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/ynny-github/toban/internal/executor/orca"
	"github.com/ynny-github/toban/internal/scaffold"
	"github.com/ynny-github/toban/internal/schedule"
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

const usage = `usage: toban [-config <dir>] <command>

設定のディレクトリ（config.yaml を置く。Tracker ごとの Workflow も config.yaml の trackers に書く）は必須。-config か環境変数 TOBAN_CONFIG で指定する。

commands:
  init <dir>          <dir>（必須）に雛形（config.yaml）を作る
  tick                すべての Workflow を 1 回ずつ処理し、Candidate を Dispatch する
  schedule install    tick を config.yaml の間隔で起動するジョブを登録する（macOS は launchd、Linux は systemd のユーザー timer）（やり直すと置き換わる）
  version             バージョンを表示する（-version でも同じ。設定のディレクトリは不要）
`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage); flag.PrintDefaults() }
	configDir := flag.String("config", os.Getenv("TOBAN_CONFIG"), "設定のディレクトリ（既定は環境変数 TOBAN_CONFIG）")
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
		err = runTick(context.Background(), log, *configDir)
	case "init":
		if err = scaffold.Init(*configDir); err == nil {
			log.Info("created", "dir", *configDir, "next", "uncomment the trackers example in config.yaml and edit it, then run `toban schedule install`")
		}
	case "schedule":
		if flag.Arg(1) != "install" {
			flag.Usage()
			os.Exit(2)
		}
		err = installSchedule(log, *configDir)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("failed", "error", err)
		os.Exit(1)
	}
}

func runTick(ctx context.Context, log *slog.Logger, configDir string) error {
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
	if err := tick.Run(ctx, log, targets); err != nil {
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

func installSchedule(log *slog.Logger, configDir string) error {
	settings, err := workflow.LoadSettings(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return err
	}
	// launchd と systemd は PATH を引き継がないので、tick が使うコマンドの場所をここで決めておく
	var tools []string
	for _, name := range []string{"gh", "orca", "git"} {
		p, err := exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("%s is not on PATH: %w", name, err)
		}
		if p, err = filepath.Abs(p); err != nil {
			return err
		}
		tools = append(tools, p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	job := schedule.Job{Toban: self, ConfigDir: configDir, Tools: tools, Interval: settings.Interval}
	switch runtime.GOOS {
	case "linux":
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		unitDir := filepath.Join(configHome, "systemd", "user")
		if err := schedule.InstallSystemd(job, unitDir); err != nil {
			return err
		}
		log.Info("installed", "units", unitDir, "interval", settings.Interval, "log", "journalctl --user -u "+schedule.Unit)
		return nil
	case "darwin":
		job.Log = filepath.Join(home, "Library", "Logs", "toban", "tick.log")
	default:
		return fmt.Errorf("schedule install supports only macOS (launchd) and Linux (systemd), not %s", runtime.GOOS)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", schedule.Label+".plist")
	if err := schedule.Install(job, plistPath); err != nil {
		return err
	}
	log.Info("installed", "plist", plistPath, "interval", settings.Interval, "log", job.Log)
	return nil
}
