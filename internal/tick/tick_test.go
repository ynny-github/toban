package tick

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/ynny-github/toban/internal/executor"
	"github.com/ynny-github/toban/internal/tracker"
)

type fakeTracker struct {
	poll     tracker.Poll
	pollErr  error
	moveErr  error
	moved    []int
	restored []int
}

func (f *fakeTracker) Poll(context.Context) (tracker.Poll, error) { return f.poll, f.pollErr }
func (f *fakeTracker) MoveToWorking(_ context.Context, i tracker.Issue) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moved = append(f.moved, i.Number)
	return nil
}
func (f *fakeTracker) Restore(_ context.Context, i tracker.Issue) error {
	f.restored = append(f.restored, i.Number)
	return nil
}
func (f *fakeTracker) Preamble(i tracker.Issue) string { return "PREAMBLE " + i.URL }

type fakeExecutor struct {
	err  error
	reqs []executor.Request
}

func (f *fakeExecutor) Dispatch(_ context.Context, r executor.Request) (executor.Result, error) {
	f.reqs = append(f.reqs, r)
	return executor.Result{Worktree: "/wt", Session: "s"}, f.err
}

func issue(n int) tracker.Issue {
	return tracker.Issue{ID: "item", Repo: "o/r", Number: n, URL: fmt.Sprintf("https://example.com/%d", n), State: "Todo"}
}

func render(i tracker.Issue) (string, error) { return "BODY", nil }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunDispatchesCandidatesUpToFreeSlots(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1), issue(2), issue(3)}, Working: 1}}
	ex := &fakeExecutor{}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 2, Render: render}})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(tr.moved, []int{1}) {
		t.Errorf("moved = %v, want [1]", tr.moved)
	}
	want := executor.Request{Issue: executor.Issue{Repo: "o/r", Number: 1}, Prompt: "PREAMBLE https://example.com/1\n\nBODY"}
	if len(ex.reqs) != 1 || ex.reqs[0] != want {
		t.Errorf("requests = %+v, want [%+v]", ex.reqs, want)
	}
}

func TestRunRestoresStateWhenLaunchFails(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1)}}}
	ex := &fakeExecutor{err: errors.New("orca is down")}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}})

	if err == nil {
		t.Error("Run succeeded although the launch failed")
	}
	if !slices.Equal(tr.moved, []int{1}) || !slices.Equal(tr.restored, []int{1}) {
		t.Errorf("moved = %v, restored = %v, want both [1]", tr.moved, tr.restored)
	}
}

func TestRunDoesNothingWhenWorkingIsFull(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1)}, Working: 2}}
	ex := &fakeExecutor{}

	if err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}); err != nil {
		t.Fatal(err)
	}
	if len(tr.moved) != 0 || len(ex.reqs) != 0 {
		t.Errorf("moved = %v, requests = %v, want none", tr.moved, ex.reqs)
	}
}

func TestRunKeepsGoingAfterAWorkflowFails(t *testing.T) {
	broken := &fakeTracker{pollErr: errors.New("gh is down")}
	ok := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1)}}}
	ex := &fakeExecutor{}

	err := Run(context.Background(), quiet, []Target{
		{Name: "broken", Tracker: broken, Executor: ex, MaxConcurrent: 1, Render: render},
		{Name: "ok", Tracker: ok, Executor: ex, MaxConcurrent: 1, Render: render},
	})

	if err == nil {
		t.Error("Run hid the failure of the broken Workflow")
	}
	if !slices.Equal(ok.moved, []int{1}) || len(ex.reqs) != 1 {
		t.Errorf("the second Workflow was not processed: moved = %v, requests = %d", ok.moved, len(ex.reqs))
	}
}
