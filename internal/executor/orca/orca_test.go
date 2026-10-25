package orca

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

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
