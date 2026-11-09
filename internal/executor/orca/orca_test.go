package orca

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ynny-github/toban/internal/executor"
)

// fakeOrca は orca のサブコマンド（先頭 2 語）ごとに testdata の JSON を返し、呼ばれた引数を記録する。
type fakeOrca struct {
	t         *testing.T
	responses map[string]string
	calls     [][]string
}

func (f *fakeOrca) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	name, ok := f.responses[strings.Join(args[:2], " ")]
	if !ok {
		f.t.Fatalf("unexpected orca call: %q", args)
	}
	return os.ReadFile("testdata/" + name)
}

func (f *fakeOrca) call(sub string) []string {
	for _, c := range f.calls {
		if strings.Join(c[:2], " ") == sub {
			return c
		}
	}
	f.t.Fatalf("orca %s was not called; calls: %q", sub, f.calls)
	return nil
}

func hasFlag(args []string, flag, value string) bool {
	i := slices.Index(args, flag)
	return i >= 0 && i+1 < len(args) && args[i+1] == value
}

func TestDispatchCreatesWorktreeWhenIssueHasNone(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{
		"worktree list":   "worktree-list.json",
		"worktree create": "worktree-create.json",
	}}
	e := New(Config{Agent: "claude"}, f.run)

	got, err := e.Dispatch(context.Background(), executor.Request{
		Issue:  executor.Issue{Repo: "ynny-github/toban", Number: 2},
		Prompt: "do the work",
	})
	if err != nil {
		t.Fatal(err)
	}

	args := f.call("worktree create")
	for _, kv := range [][2]string{
		{"--project", "github:ynny-github/toban"},
		{"--issue", "2"},
		{"--agent", "claude"},
		{"--prompt", "do the work"},
		{"--surface", "terminal"},
	} {
		if !hasFlag(args, kv[0], kv[1]) {
			t.Errorf("worktree create args %q lack %s %s", args, kv[0], kv[1])
		}
	}
	want := executor.Result{
		Worktree: "/home/dev/orca/workspaces/toban/gh-1",
		Session:  "term_9e8b24d4-2168-49a3-adbb-a49e5943b5a4",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDispatchLaunchesAgentInExistingWorktree(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{
		"worktree list": "worktree-list.json",
		"worktree ps":   "worktree-ps-none.json",
		"agent launch":  "agent-launch.json",
	}}
	e := New(Config{Agent: "claude"}, f.run)

	got, err := e.Dispatch(context.Background(), executor.Request{
		Issue:  executor.Issue{Repo: "ynny-github/toban", Number: 1},
		Prompt: "continue",
	})
	if err != nil {
		t.Fatal(err)
	}

	args := f.call("agent launch")
	for _, kv := range [][2]string{
		{"--worktree", "path:/home/dev/orca/workspaces/toban/gh-1"},
		{"--agent", "claude"},
		{"--prompt", "continue"},
		{"--surface", "terminal"},
	} {
		if !hasFlag(args, kv[0], kv[1]) {
			t.Errorf("agent launch args %q lack %s %s", args, kv[0], kv[1])
		}
	}
	want := executor.Result{
		Worktree: "/home/dev/orca/workspaces/toban/gh-1",
		Session:  "structured-agent-session-claude_05800df8_38b1_4ebf_9909_b76daded3b62",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDispatchDoesNotReuseWorktreeOfSameNumberInAnotherRepo(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{
		"worktree list":   "worktree-list.json", // ynny-github/toban#1 と example/other#1 に worktree がある
		"worktree create": "worktree-create.json",
	}}
	e := New(Config{Agent: "claude"}, f.run)

	if _, err := e.Dispatch(context.Background(), executor.Request{
		Issue: executor.Issue{Repo: "ynny-github/dotfiles", Number: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if !hasFlag(f.call("worktree create"), "--project", "github:ynny-github/dotfiles") {
		t.Errorf("worktree create was not called for ynny-github/dotfiles: %q", f.calls)
	}
}

func TestDispatchReportsOrcaError(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{
		"worktree list":   "worktree-list.json",
		"worktree create": "error.json",
	}}
	e := New(Config{Agent: "claude"}, f.run)

	_, err := e.Dispatch(context.Background(), executor.Request{
		Issue: executor.Issue{Repo: "ynny-github/toban", Number: 2},
	})
	if err == nil || !strings.Contains(err.Error(), "terminal_handle_stale") {
		t.Errorf("got error %v, want one naming the Orca error code", err)
	}
}

// 一覧が切られていると、Issue の worktree を見落として二重に作ってしまう。
func TestDispatchRefusesTruncatedWorktreeList(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{
		"worktree list": "worktree-list-truncated.json",
	}}
	e := New(Config{Agent: "claude"}, f.run)

	if _, err := e.Dispatch(context.Background(), executor.Request{
		Issue: executor.Issue{Repo: "ynny-github/toban", Number: 2},
	}); err == nil {
		t.Error("Dispatch succeeded on a truncated worktree list")
	}
}

// ---- 生きているセッションがある worktree（ADR 0006）----

var gh1 = executor.Issue{Repo: "ynny-github/toban", Number: 1} // worktree-list.json では gh-1 に紐づく

func sessionRequest() executor.Request {
	return executor.Request{Issue: gh1, Prompt: "first prompt", ContinuationPrompt: "keep going"}
}

func sessionFake(t *testing.T, ps, send string) *fakeOrca {
	return &fakeOrca{t: t, responses: map[string]string{
		"worktree list": "worktree-list.json",
		"worktree ps":   ps,
		"terminal list": "terminal-list.json",
		"terminal send": send,
	}}
}

func TestDispatchSendsOnlyTheContinuationToAFinishedSession(t *testing.T) {
	f := sessionFake(t, "worktree-ps-done.json", "terminal-send.json")

	got, err := New(Config{Agent: "claude"}, f.run).Dispatch(context.Background(), sessionRequest())
	if err != nil {
		t.Fatal(err)
	}

	args := f.call("terminal send")
	for _, kv := range [][2]string{{"--terminal", "term_gh1"}, {"--text", "keep going"}} {
		if !hasFlag(args, kv[0], kv[1]) {
			t.Errorf("terminal send args %q lack %s %s", args, kv[0], kv[1])
		}
	}
	if !slices.Contains(args, "--enter") || !slices.Contains(args, "--wait-submit") {
		t.Errorf("terminal send args %q lack --enter or --wait-submit", args)
	}
	for _, c := range f.calls {
		if c[0] == "agent" || c[0] == "worktree" && c[1] == "create" {
			t.Errorf("started a new agent although a session was alive: %q", c)
		}
	}
	if want := (executor.Result{Worktree: "/home/dev/orca/workspaces/toban/gh-1", Session: "term_gh1"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCheckReportsBusyForAWorkingSession(t *testing.T) {
	f := sessionFake(t, "worktree-ps-working.json", "terminal-send.json")

	err := New(Config{Agent: "claude"}, f.run).Check(context.Background(), gh1)

	if !errors.Is(err, executor.ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
}

func TestDispatchLaunchesWhenNoLiveSessionOfThisAgent(t *testing.T) {
	// dead: 端末が終わった claude のセッションと、別の種類（codex）の作業中のセッションだけがある
	// exited: claude を /exit で終えてシェルだけが残っている（hasLivePty はあるが programStatus がない）
	for _, ps := range []string{"worktree-ps-dead.json", "worktree-ps-exited.json"} {
		f := &fakeOrca{t: t, responses: map[string]string{
			"worktree list": "worktree-list.json",
			"worktree ps":   ps,
			"agent launch":  "agent-launch.json",
		}}

		if _, err := New(Config{Agent: "claude"}, f.run).Dispatch(context.Background(), sessionRequest()); err != nil {
			t.Fatalf("%s: %v", ps, err)
		}
		if !hasFlag(f.call("agent launch"), "--prompt", "first prompt") {
			t.Errorf("%s: a new session did not get the first prompt: %q", ps, f.calls)
		}
	}
}

func TestDispatchRefusesWhenItCannotTellWhatToDo(t *testing.T) {
	for _, c := range []struct{ name, ps, send, want string }{
		{"unknown state", "worktree-ps-unknown.json", "terminal-send.json", "unknown state"},
		{"two sessions", "worktree-ps-two.json", "terminal-send.json", "2 live"},
		{"truncated", "worktree-ps-truncated.json", "terminal-send.json", "truncated"},
		{"not accepted", "worktree-ps-done.json", "terminal-send-not-accepted.json", "did not accept"},
		{"permission menu", "worktree-ps-done.json", "terminal-send-blocked.json", "agent_prompt_blocked"},
		{"stale handle", "worktree-ps-done.json", "error.json", "terminal_handle_stale"},
	} {
		f := sessionFake(t, c.ps, c.send)

		_, err := New(Config{Agent: "claude"}, f.run).Dispatch(context.Background(), sessionRequest())

		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", c.name, err, c.want)
		}
		for _, call := range f.calls { // 新しく起動して二重にしない
			if call[0] == "agent" || call[0] == "worktree" && call[1] == "create" {
				t.Errorf("%s: started a new agent: %q", c.name, call)
			}
		}
	}
}

// ---- Working の Issue の継続 ----

func TestSessionReportsWhenTheSessionLastChanged(t *testing.T) {
	for _, c := range []struct {
		ps   string
		want executor.Session
	}{
		{"worktree-ps-done.json", executor.Session{State: "done", Since: time.UnixMilli(1794099333257)}},
		// 動きがない時間は、updatedAt（1794099370400）と lastOutputAt の遅いほうから数える
		{"worktree-ps-working.json", executor.Session{State: "working", Since: time.UnixMilli(1794099400000)}},
		// 落ちた: 端末を閉じた（agents が空）、claude だけ終わってシェルが残った（programStatus がない）
		{"worktree-ps-dead.json", executor.Session{State: "dead", Since: time.UnixMilli(1794099370400)}},
		{"worktree-ps-exited.json", executor.Session{State: "dead", Since: time.UnixMilli(1794099370400)}},
		{"worktree-ps-none.json", executor.Session{State: "dead"}},
		{"worktree-ps-working-no-times.json", executor.Session{State: "working"}},
	} {
		f := sessionFake(t, c.ps, "terminal-send.json")

		got, err := New(Config{Agent: "claude"}, f.run).Session(context.Background(), gh1)

		if err != nil || !got.Since.Equal(c.want.Since) || got.State != c.want.State {
			t.Errorf("%s: Session = %+v, %v; want %+v", c.ps, got, err, c.want)
		}
	}
}

func TestSessionRefusesWhenItCannotTell(t *testing.T) {
	for _, ps := range []string{"worktree-ps-unknown.json", "worktree-ps-two.json", "worktree-ps-truncated.json"} {
		f := sessionFake(t, ps, "terminal-send.json")
		if _, err := New(Config{Agent: "claude"}, f.run).Session(context.Background(), gh1); err == nil {
			t.Errorf("%s: Session succeeded", ps)
		}
	}
}

func TestContinueSendsThePromptToTheFinishedSession(t *testing.T) {
	f := sessionFake(t, "worktree-ps-done.json", "terminal-send.json")

	if _, err := New(Config{Agent: "claude"}, f.run).Continue(context.Background(), gh1, "keep going"); err != nil {
		t.Fatal(err)
	}
	if args := f.call("terminal send"); !hasFlag(args, "--text", "keep going") || !slices.Contains(args, "--wait-submit") {
		t.Errorf("terminal send args = %q", args)
	}
}

func TestContinueSendsToAWorkingSession(t *testing.T) {
	f := sessionFake(t, "worktree-ps-working.json", "terminal-send.json")

	if _, err := New(Config{Agent: "claude"}, f.run).Continue(context.Background(), gh1, "keep going"); err != nil {
		t.Fatal(err)
	}
	if args := f.call("terminal send"); !hasFlag(args, "--text", "keep going") {
		t.Errorf("terminal send args = %q", args)
	}
}

func TestContinueNeverLaunches(t *testing.T) {
	f := sessionFake(t, "worktree-ps-dead.json", "terminal-send.json")

	if _, err := New(Config{Agent: "claude"}, f.run).Continue(context.Background(), gh1, "keep going"); err == nil {
		t.Error("Continue succeeded without a live session")
	}
	for _, c := range f.calls {
		if c[0] == "agent" || c[0] == "terminal" {
			t.Errorf("unexpected call %q", c)
		}
	}
}

func TestContinueReportsBlockedForAPermissionMenu(t *testing.T) {
	f := sessionFake(t, "worktree-ps-done.json", "terminal-send-blocked.json")

	_, err := New(Config{Agent: "claude"}, f.run).Continue(context.Background(), gh1, "keep going")

	if !errors.Is(err, executor.ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
}
