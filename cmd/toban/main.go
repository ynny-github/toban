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

	"github.com/ynny-github/toban/internal/executor/orca"
	"github.com/ynny-github/toban/internal/schedule"
	"github.com/ynny-github/toban/internal/tick"
	"github.com/ynny-github/toban/internal/tracker/github"
	"github.com/ynny-github/toban/internal/workflow"
)

const usage = `usage: toban [-config <dir>] <command>

commands:
  tick                すべての Workflow を 1 回ずつ処理し、Candidate を Dispatch する
  schedule install    tick を config.yaml の間隔で起動するジョブを launchd に登録する（やり直すと置き換わる）
`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage); flag.PrintDefaults() }
	configDir := flag.String("config", defaultConfigDir(), "設定のディレクトリ")
	flag.Parse()

	var err error
	switch flag.Arg(0) {
	case "tick":
		err = runTick(context.Background(), log, *configDir)
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

func defaultConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "toban")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "toban")
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

	// 読めない Workflow があっても、読めたものは処理する
	workflows, loadErr := workflow.LoadDir(filepath.Join(configDir, "workflows"))
	var targets []tick.Target
	var errs []error
	if loadErr != nil {
		errs = append(errs, loadErr)
	}
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
	t := tick.Target{Name: w.Path, MaxConcurrent: w.MaxConcurrent, Render: w.Render}
	switch w.Tracker.Kind {
	case "github":
		tr, err := github.New(*w.Tracker.GitHub, github.Command("gh"))
		if err != nil {
			return t, fmt.Errorf("%s: %w", w.Path, err)
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
	// launchd は PATH を引き継がないので、tick が使うコマンドの場所をここで決めておく
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
	job := schedule.Job{
		Toban: self, ConfigDir: configDir, Tools: tools, Interval: settings.Interval,
		Log: filepath.Join(home, "Library", "Logs", "toban", "tick.log"),
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", schedule.Label+".plist")
	if err := schedule.Install(job, plistPath); err != nil {
		return err
	}
	log.Info("installed", "plist", plistPath, "interval", settings.Interval, "log", job.Log)
	return nil
}
