// Package orca は Orca の CLI で worker を起動する Executor。
package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"time"

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
// state は pane のセッションの state。since は、done なら done に入った時刻、working と pane がないときは worktree で最後に動いた時刻（Unix ミリ秒）。
type plan struct {
	path, pane, state string
	since             int64
}

func (e *Executor) plan(ctx context.Context, issue executor.Issue) (plan, error) {
	path, err := e.findWorktree(ctx, issue)
	if err != nil || path == "" {
		return plan{}, err
	}
	live, active, err := e.liveAgents(ctx, path)
	if err != nil {
		return plan{}, err
	}
	switch len(live) {
	case 0:
		return plan{path: path, since: active}, nil
	case 1:
		p := plan{path: path, pane: live[0].PaneKey, state: live[0].State}
		switch p.state {
		case "done":
			p.since = live[0].StateStartedAt
			return p, nil
		case "working":
			p.since = active
			return p, nil
		}
		return plan{}, fmt.Errorf("%s has a %s session in the unknown state %q; not sending", path, e.cfg.Agent, live[0].State)
	}
	return plan{}, fmt.Errorf("%s has %d live %s sessions; cannot tell which to send to", path, len(live), e.cfg.Agent)
}

type agentInfo struct {
	PaneKey        string `json:"paneKey"` // "<tabId>:<leafId>"
	State          string `json:"state"`
	StateStartedAt int64  `json:"stateStartedAt"` // Unix ミリ秒
	UpdatedAt      int64  `json:"updatedAt"`      // Unix ミリ秒。ツール呼び出しのたびに更新される
	AgentType      string `json:"agentType"`
	HasLivePty     bool   `json:"hasLivePty"`
	// ProgramStatus は claude が動いている間だけある。/exit のあとシェルだけ残ると、hasLivePty のままこれが消える
	ProgramStatus *struct{} `json:"programStatus"`
}

// liveAgents は path の worktree で生きている、設定した種類のエージェントのセッションと、
// その worktree で最後に動きがあった時刻（Unix ミリ秒。max(agents[].updatedAt, lastOutputAt)）を返す。
func (e *Executor) liveAgents(ctx context.Context, path string) (live []agentInfo, active int64, err error) {
	ps, err := e.ps(ctx, path)
	if err != nil {
		return nil, 0, err
	}
	for _, w := range ps {
		if w.Path != path {
			continue
		}
		active = max(active, w.LastOutputAt)
		for _, a := range w.Agents {
			active = max(active, a.UpdatedAt)
			if a.AgentType == e.cfg.Agent && a.HasLivePty && a.ProgramStatus != nil {
				live = append(live, a)
			}
		}
	}
	return live, active, nil
}

type psWorktree struct {
	Path         string      `json:"path"`
	LastOutputAt int64       `json:"lastOutputAt"` // Unix ミリ秒。出力のたびに更新される
	Agents       []agentInfo `json:"agents"`
}

// ps は orca worktree ps の worktrees を返す。about はエラーに出す、確かめたかった worktree。
func (e *Executor) ps(ctx context.Context, about string) ([]psWorktree, error) {
	var ps struct {
		Worktrees []psWorktree `json:"worktrees"`
		Truncated bool         `json:"truncated"`
	}
	if err := e.call(ctx, &ps, "worktree", "ps", "--limit", "1000", "--json"); err != nil {
		return nil, err
	}
	if ps.Truncated {
		return nil, fmt.Errorf("orca worktree ps: result is truncated; cannot tell whether %s has a live session", about)
	}
	return ps.Worktrees, nil
}

// planIdle は plan と同じだが、作業中のセッションがあれば ErrBusy を返す。Dispatch はそこへ送らない（ADR 0006）。
func (e *Executor) planIdle(ctx context.Context, issue executor.Issue) (plan, error) {
	p, err := e.plan(ctx, issue)
	if err == nil && p.state == "working" {
		return plan{}, executor.ErrBusy
	}
	return p, err
}

func (e *Executor) Check(ctx context.Context, issue executor.Issue) error {
	_, err := e.planIdle(ctx, issue)
	return err
}

func (e *Executor) Session(ctx context.Context, issue executor.Issue) (executor.Session, error) {
	p, err := e.plan(ctx, issue)
	if err != nil || p.path == "" {
		return executor.Session{}, err
	}
	if p.pane == "" { // worktree はあるのに生きたセッションがない（落ちた）
		return executor.Session{State: "dead", Since: unixMilli(p.since)}, nil
	}
	if p.since <= 0 && p.state == "working" { // 動いた時刻が分からない。Since を空にして、tick に送らせない
		return executor.Session{State: "working"}, nil
	}
	if p.since <= 0 { // 時刻が分からないまま、すぐ送ってしまわないように
		return executor.Session{}, fmt.Errorf("%s: orca worktree ps did not report when the session became done", p.path)
	}
	return executor.Session{State: p.state, Since: time.UnixMilli(p.since)}, nil
}

// Continue は送る直前にもう一度セッションを確かめる。done でも working でも送る（working なら次のターンまで積まれる）。
func (e *Executor) Continue(ctx context.Context, issue executor.Issue, prompt string) (executor.Result, error) {
	p, err := e.plan(ctx, issue)
	if err != nil {
		return executor.Result{}, err
	}
	if p.pane == "" {
		return executor.Result{}, fmt.Errorf("%s#%d has no live session to continue", issue.Repo, issue.Number)
	}
	return e.send(ctx, p, prompt)
}

func (e *Executor) Dispatch(ctx context.Context, req executor.Request) (executor.Result, error) {
	p, err := e.planIdle(ctx, req.Issue)
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
func (e *Executor) findWorktree(ctx context.Context, issue executor.Issue) (string, error) {
	list, err := e.worktrees(ctx, fmt.Sprintf("whether %s#%d already has a worktree", issue.Repo, issue.Number))
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.linkedTo(issue) {
			return w.Path, nil
		}
	}
	return "", nil
}

type worktree struct {
	ProjectID      string `json:"projectId"`
	Path           string `json:"path"`
	IsMainWorktree bool   `json:"isMainWorktree"`
	LinkedItems    []struct {
		Provider string `json:"provider"`
		Type     string `json:"type"`
		Number   int    `json:"number"`
	} `json:"linkedItems"`
}

// linkedTo は worktree が issue に紐付いているかを返す。
// linkedItems には issue の番号しか入らないので、リポジトリは worktree の projectId で照合する。
func (w worktree) linkedTo(issue executor.Issue) bool {
	if w.ProjectID != "github:"+issue.Repo {
		return false
	}
	for _, l := range w.LinkedItems {
		if l.Provider == "github" && l.Type == "issue" && l.Number == issue.Number {
			return true
		}
	}
	return false
}

// worktrees は orca worktree list の worktrees を返す。what は切り詰められたときのエラーに出す、確かめたかったこと。
func (e *Executor) worktrees(ctx context.Context, what string) ([]worktree, error) {
	var list struct {
		Worktrees []worktree `json:"worktrees"`
		Truncated bool       `json:"truncated"`
	}
	if err := e.call(ctx, &list, "worktree", "list", "--json"); err != nil {
		return nil, err
	}
	if list.Truncated {
		return nil, fmt.Errorf("orca worktree list: result is truncated; cannot tell %s", what)
	}
	return list.Worktrees, nil
}

// Remove は issues に紐付く worktree をすべて orca worktree rm で消す（ADR 0009）。端末も一緒に消える。
// メインの worktree と、done 以外（working や未知の state）の生きたセッションがある worktree は残す。--force は付けないので、
// コミット前の変更がある worktree は消えず、そのエラーを返す（次の tick でまた試す）。
func (e *Executor) Remove(ctx context.Context, issues []executor.Issue) (removed []string, err error) {
	if len(issues) == 0 {
		return nil, nil
	}
	list, err := e.worktrees(ctx, "which worktrees belong to closed issues")
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, w := range list {
		if !w.IsMainWorktree && slices.ContainsFunc(issues, w.linkedTo) {
			targets = append(targets, w.Path)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	ps, err := e.ps(ctx, "a worktree of a closed issue")
	if err != nil {
		return nil, err
	}
	working := map[string]bool{} // done になっていない生きたセッションがある worktree
	for _, w := range ps {
		for _, a := range w.Agents {
			// 種類によらず、作業中（や未知の state）なら done まで待つ。/exit のあとシェルだけ残ったもの（programStatus なし）は落ちている
			if a.HasLivePty && a.ProgramStatus != nil && a.State != "done" {
				working[w.Path] = true
			}
		}
	}
	var errs []error
	for _, path := range targets {
		if working[path] {
			continue
		}
		if err := e.call(ctx, nil, "worktree", "rm", "--worktree", "path:"+path, "--json"); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		removed = append(removed, path)
	}
	return removed, errors.Join(errs...)
}

// call は orca を実行し、--json の応答の result を out に読み込む。out が nil なら result は読まない。
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
		err := fmt.Errorf("orca %s %s: %s: %s", args[0], args[1], env.Error.Code, env.Error.Message)
		if env.Error.Code == "agent_prompt_blocked" {
			return fmt.Errorf("%w: %w", executor.ErrBlocked, err)
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// unixMilli は 0 以下（分からない）を空の時刻にする。
func unixMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
