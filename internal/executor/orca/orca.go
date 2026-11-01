// Package orca は Orca の CLI で worker を起動する Executor。
package orca

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/ynny-github/toban/internal/executor"
)

type Config struct {
	Agent string `yaml:"agent"`
}

// Runner は orca コマンドを引数つきで実行し、標準出力を返す。
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Command は bin（orca の実行ファイル）を起動する Runner を返す。
// 失敗しても --json の応答は標準出力に出るので、エラーと一緒に返す。
func Command(bin string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, bin, args...).Output()
	}
}

type Executor struct {
	cfg Config
	run Runner
}

func New(cfg Config, run Runner) *Executor { return &Executor{cfg: cfg, run: run} }

func (e *Executor) Dispatch(ctx context.Context, req executor.Request) (executor.Result, error) {
	path, err := e.findWorktree(ctx, req.Issue)
	if err != nil {
		return executor.Result{}, err
	}
	if path != "" {
		var launched struct {
			Outcome struct {
				Handle string `json:"handle"`
			} `json:"outcome"`
		}
		err := e.call(ctx, &launched, "agent", "launch",
			"--worktree", "path:"+path,
			"--agent", e.cfg.Agent,
			"--prompt", req.Prompt,
			"--json")
		if err != nil {
			return executor.Result{}, err
		}
		return executor.Result{Worktree: path, Session: launched.Outcome.Handle}, nil
	}

	var created struct {
		Worktree struct {
			Path string `json:"path"`
		} `json:"worktree"`
		Launch struct {
			Outcome struct {
				Handle string `json:"handle"`
			} `json:"outcome"`
		} `json:"launch"`
	}
	n := strconv.Itoa(req.Issue.Number)
	err = e.call(ctx, &created, "worktree", "create",
		"--name", "issue-"+n,
		"--project", "github:"+req.Issue.Repo,
		"--issue", n,
		"--agent", e.cfg.Agent,
		"--prompt", req.Prompt,
		"--no-parent", "--json")
	if err != nil {
		return executor.Result{}, err
	}
	return executor.Result{Worktree: created.Worktree.Path, Session: created.Launch.Outcome.Handle}, nil
}

// findWorktree は Issue に紐付いた worktree のパスを返す。なければ空文字列。
// linkedItems には issue の番号しか入らないので、リポジトリは worktree の projectId で照合する。
func (e *Executor) findWorktree(ctx context.Context, issue executor.Issue) (string, error) {
	var list struct {
		Worktrees []struct {
			ProjectID   string `json:"projectId"`
			Path        string `json:"path"`
			LinkedItems []struct {
				Provider string `json:"provider"`
				Type     string `json:"type"`
				Number   int    `json:"number"`
			} `json:"linkedItems"`
		} `json:"worktrees"`
		Truncated bool `json:"truncated"`
	}
	if err := e.call(ctx, &list, "worktree", "list", "--json"); err != nil {
		return "", err
	}
	if list.Truncated {
		return "", fmt.Errorf("orca worktree list: result is truncated; cannot tell whether %s#%d already has a worktree", issue.Repo, issue.Number)
	}
	for _, w := range list.Worktrees {
		if w.ProjectID != "github:"+issue.Repo {
			continue
		}
		for _, l := range w.LinkedItems {
			if l.Provider == "github" && l.Type == "issue" && l.Number == issue.Number {
				return w.Path, nil
			}
		}
	}
	return "", nil
}

// call は orca を実行し、--json の応答の result を out に読み込む。
func (e *Executor) call(ctx context.Context, out any, args ...string) error {
	stdout, runErr := e.run(ctx, args...)
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		if runErr != nil {
			return fmt.Errorf("orca %s %s: %w", args[0], args[1], runErr)
		}
		return fmt.Errorf("orca %s %s: unreadable output: %w", args[0], args[1], err)
	}
	if !env.OK {
		return fmt.Errorf("orca %s %s: %s: %s", args[0], args[1], env.Error.Code, env.Error.Message)
	}
	return json.Unmarshal(env.Result, out)
}
