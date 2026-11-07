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

// plan は Issue の作業場所と、続きの指示を送る先のセッションを決める。
// path が空なら worktree を作り、pane が空なら新しく起動し、pane があればそのセッションへ送る（ADR 0006）。
type plan struct{ path, pane string }

func (e *Executor) plan(ctx context.Context, issue executor.Issue) (plan, error) {
	path, err := e.findWorktree(ctx, issue)
	if err != nil || path == "" {
		return plan{}, err
	}
	live, err := e.liveAgents(ctx, path)
	if err != nil {
		return plan{}, err
	}
	switch len(live) {
	case 0:
		return plan{path: path}, nil
	case 1:
		switch live[0].State {
		case "done":
			return plan{path: path, pane: live[0].PaneKey}, nil
		case "working":
			return plan{}, executor.ErrBusy
		}
		return plan{}, fmt.Errorf("%s has a %s session in the unknown state %q; not sending", path, e.cfg.Agent, live[0].State)
	}
	return plan{}, fmt.Errorf("%s has %d live %s sessions; cannot tell which to send to", path, len(live), e.cfg.Agent)
}

type agentInfo struct {
	PaneKey    string `json:"paneKey"` // "<tabId>:<leafId>"
	State      string `json:"state"`
	AgentType  string `json:"agentType"`
	HasLivePty bool   `json:"hasLivePty"`
}

// liveAgents は path の worktree で生きている、設定した種類のエージェントのセッションを返す。
func (e *Executor) liveAgents(ctx context.Context, path string) ([]agentInfo, error) {
	var ps struct {
		Worktrees []struct {
			Path   string      `json:"path"`
			Agents []agentInfo `json:"agents"`
		} `json:"worktrees"`
		Truncated bool `json:"truncated"`
	}
	if err := e.call(ctx, &ps, "worktree", "ps", "--limit", "1000", "--json"); err != nil {
		return nil, err
	}
	if ps.Truncated {
		return nil, fmt.Errorf("orca worktree ps: result is truncated; cannot tell whether %s has a live session", path)
	}
	var live []agentInfo
	for _, w := range ps.Worktrees {
		if w.Path != path {
			continue
		}
		for _, a := range w.Agents {
			if a.AgentType == e.cfg.Agent && a.HasLivePty {
				live = append(live, a)
			}
		}
	}
	return live, nil
}

func (e *Executor) Check(ctx context.Context, issue executor.Issue) error {
	_, err := e.plan(ctx, issue)
	return err
}

func (e *Executor) Dispatch(ctx context.Context, req executor.Request) (executor.Result, error) {
	p, err := e.plan(ctx, req.Issue)
	if err != nil {
		return executor.Result{}, err
	}
	if p.pane != "" {
		return e.send(ctx, p, req.ContinuationPrompt)
	}
	if p.path != "" {
		var launched struct {
			Outcome struct {
				Handle string `json:"handle"`
			} `json:"outcome"`
		}
		err := e.call(ctx, &launched, "agent", "launch",
			"--worktree", "path:"+p.path,
			"--agent", e.cfg.Agent,
			"--prompt", req.Prompt,
			"--surface", "terminal", // チャット画面だと、あとで terminal send が届かない
			"--json")
		if err != nil {
			return executor.Result{}, err
		}
		return executor.Result{Worktree: p.path, Session: launched.Outcome.Handle}, nil
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
		"--surface", "terminal",
		"--no-parent", "--json")
	if err != nil {
		return executor.Result{}, err
	}
	return executor.Result{Worktree: created.Worktree.Path, Session: created.Launch.Outcome.Handle}, nil
}

// send は生きているセッションに続きの指示を送る。受け付けられなければ失敗にし、新しく起動はしない。
func (e *Executor) send(ctx context.Context, p plan, prompt string) (executor.Result, error) {
	if prompt == "" {
		return executor.Result{}, fmt.Errorf("no continuation prompt to send to %s", p.path)
	}
	var list struct {
		Terminals []struct {
			Handle string `json:"handle"`
			TabID  string `json:"tabId"`
			LeafID string `json:"leafId"`
		} `json:"terminals"`
	}
	if err := e.call(ctx, &list, "terminal", "list", "--worktree", "path:"+p.path, "--json"); err != nil {
		return executor.Result{}, err
	}
	handle := ""
	for _, t := range list.Terminals {
		if t.TabID+":"+t.LeafID == p.pane {
			handle = t.Handle
		}
	}
	if handle == "" {
		return executor.Result{}, fmt.Errorf("orca terminal list: no terminal for the agent pane %s in %s", p.pane, p.path)
	}
	var sent struct {
		Send struct {
			Accepted bool `json:"accepted"`
		} `json:"send"`
	}
	if err := e.call(ctx, &sent, "terminal", "send",
		"--terminal", handle, "--text", prompt, "--enter", "--wait-submit", "10", "--json"); err != nil {
		return executor.Result{}, err
	}
	if !sent.Send.Accepted {
		return executor.Result{}, fmt.Errorf("orca terminal send: %s did not accept the prompt", handle)
	}
	return executor.Result{Worktree: p.path, Session: handle}, nil
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
