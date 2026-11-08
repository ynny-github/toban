package tick

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/ynny-github/toban/internal/executor"
	"github.com/ynny-github/toban/internal/state"
	"github.com/ynny-github/toban/internal/tracker"
)

type fakeTracker struct {
	poll     tracker.Poll
	pollErr  error
	moveErr  error
	missing  map[int]bool // WORKFLOW.md がない Issue
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
func (f *fakeTracker) CheckFile(_ context.Context, i tracker.Issue, path string) error {
	if f.missing[i.Number] {
		return fmt.Errorf("%s has no %s", i.Repo, path)
	}
	return nil
}
func (f *fakeTracker) Preamble(i tracker.Issue) string { return "PREAMBLE " + i.URL }

type fakeExecutor struct {
	err        error
	checkErr   map[int]error // Issue の番号ごとの Check の結果
	reqs       []executor.Request
	sessions   map[int]executor.Session
	sessionErr error
	sendErr    error
	sent       []string // Continue に渡された "<番号> <prompt>"
}

func (f *fakeExecutor) Session(_ context.Context, i executor.Issue) (executor.Session, error) {
	return f.sessions[i.Number], f.sessionErr
}

func (f *fakeExecutor) Continue(_ context.Context, i executor.Issue, prompt string) (executor.Result, error) {
	if f.sendErr != nil {
		return executor.Result{}, f.sendErr
	}
	f.sent = append(f.sent, fmt.Sprintf("%d %s", i.Number, prompt))
	return executor.Result{Worktree: "/wt", Session: "s"}, nil
}

func (f *fakeExecutor) Check(_ context.Context, i executor.Issue) error { return f.checkErr[i.Number] }

func (f *fakeExecutor) Dispatch(_ context.Context, r executor.Request) (executor.Result, error) {
	f.reqs = append(f.reqs, r)
	return executor.Result{Worktree: "/wt", Session: "s"}, f.err
}

func issue(n int) tracker.Issue {
	return tracker.Issue{ID: "item", Repo: "o/r", Number: n, URL: fmt.Sprintf("https://example.com/%d", n), State: "Todo"}
}

func render(i tracker.Issue, continuation bool) (string, error) {
	if continuation {
		return "CONTINUE", nil
	}
	return "BODY", nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunDispatchesCandidatesUpToFreeSlots(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1), issue(2), issue(3)}, Working: 1}}
	ex := &fakeExecutor{}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 2, Render: render}}, settings(nil))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(tr.moved, []int{1}) {
		t.Errorf("moved = %v, want [1]", tr.moved)
	}
	want := executor.Request{Issue: executor.Issue{Repo: "o/r", Number: 1}, Prompt: "PREAMBLE https://example.com/1\n\nBODY", ContinuationPrompt: "CONTINUE"}
	if len(ex.reqs) != 1 || ex.reqs[0] != want {
		t.Errorf("requests = %+v, want [%+v]", ex.reqs, want)
	}
}

func TestRunRestoresStateWhenLaunchFails(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1)}}}
	ex := &fakeExecutor{err: errors.New("orca is down")}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}, settings(nil))

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

	if err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}, settings(nil)); err != nil {
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
	}, settings(nil))

	if err == nil {
		t.Error("Run hid the failure of the broken Workflow")
	}
	if !slices.Equal(ok.moved, []int{1}) || len(ex.reqs) != 1 {
		t.Errorf("the second Workflow was not processed: moved = %v, requests = %d", ok.moved, len(ex.reqs))
	}
}

func TestRunSkipsIssuesWithoutWorkflowFileAndTakesTheNext(t *testing.T) {
	tr := &fakeTracker{
		poll:    tracker.Poll{Candidates: []tracker.Issue{issue(1), issue(2), issue(3)}},
		missing: map[int]bool{1: true},
	}
	ex := &fakeExecutor{}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}, settings(nil))

	if err == nil {
		t.Error("Run hid the Issue without WORKFLOW.md")
	}
	if !slices.Equal(tr.moved, []int{2}) || len(tr.restored) != 0 || len(ex.reqs) != 1 {
		t.Errorf("moved = %v, restored = %v, requests = %d, want only #2 dispatched", tr.moved, tr.restored, len(ex.reqs))
	}
}

func TestRunStopsAfterALaunchFails(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1), issue(2)}}}
	ex := &fakeExecutor{err: errors.New("orca is down")}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 2, Render: render}}, settings(nil))

	if err == nil {
		t.Error("Run succeeded although the launch failed")
	}
	if !slices.Equal(tr.moved, []int{1}) {
		t.Errorf("moved = %v, want [1]: a failing Executor should not be tried on every Candidate", tr.moved)
	}
}

func TestRunLeavesAWorkingSessionAloneAndTakesTheNext(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1), issue(2)}}}
	ex := &fakeExecutor{checkErr: map[int]error{1: executor.ErrBusy}}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}, settings(nil))

	if err != nil {
		t.Errorf("a session that is still working is not a failure: %v", err)
	}
	if !slices.Equal(tr.moved, []int{2}) || len(tr.restored) != 0 {
		t.Errorf("moved = %v, restored = %v, want only #2 moved and nothing restored", tr.moved, tr.restored)
	}
}

func TestRunDoesNotMoveStateWhenTheExecutorCannotDispatch(t *testing.T) {
	tr := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1)}}}
	ex := &fakeExecutor{checkErr: map[int]error{1: errors.New("two live sessions")}}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}, settings(nil))

	if err == nil {
		t.Error("Run hid the Executor's refusal")
	}
	if len(tr.moved) != 0 || len(ex.reqs) != 0 {
		t.Errorf("moved = %v, requests = %d, want none", tr.moved, len(ex.reqs))
	}
}

// ---- Working の Issue の継続 ----

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func settings(hints state.State) Continuation {
	if hints == nil {
		hints = state.State{}
	}
	return Continuation{After: 10 * time.Minute, Max: 5, Now: now, Hints: hints}
}

func doneFor(d time.Duration) executor.Session {
	return executor.Session{State: "done", Since: now.Add(-d)}
}

func continueTarget(ex *fakeExecutor, poll tracker.Poll) (Target, *fakeTracker) {
	tr := &fakeTracker{poll: poll}
	return Target{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}, tr
}

func working(ns ...int) tracker.Poll {
	p := tracker.Poll{Working: len(ns)}
	for _, n := range ns {
		p.WorkingIssues = append(p.WorkingIssues, issue(n))
	}
	return p
}

func TestRunContinuesASessionDoneLongEnough(t *testing.T) {
	ex := &fakeExecutor{sessions: map[int]executor.Session{1: doneFor(10 * time.Minute), 2: doneFor(9 * time.Minute)}}
	tg, tr := continueTarget(ex, working(1, 2))
	hints := state.State{"o/r#1": {Continuations: 1}}

	if err := Run(context.Background(), quiet, []Target{tg}, settings(hints)); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(ex.sent, []string{"1 CONTINUE"}) {
		t.Errorf("sent = %q, want only the continuation to #1", ex.sent)
	}
	if e := hints["o/r#1"]; e.Continuations != 2 || !e.LastSentAt.Equal(now) {
		t.Errorf("hint = %+v, want 2 continuations sent at %v", e, now)
	}
	if _, ok := hints["o/r#2"]; ok {
		t.Errorf("#2 got a hint although nothing was sent: %+v", hints)
	}
	if len(tr.moved) != 0 || len(tr.restored) != 0 || len(ex.reqs) != 0 {
		t.Errorf("moved = %v, restored = %v, dispatched = %d; want the State untouched", tr.moved, tr.restored, len(ex.reqs))
	}
}

func TestRunDoesNotContinue(t *testing.T) {
	long := 20 * time.Minute
	for _, c := range []struct {
		name string
		ex   *fakeExecutor
		hint state.Entry
	}{
		{"limit reached", &fakeExecutor{sessions: map[int]executor.Session{1: doneFor(long)}}, state.Entry{Continuations: 5}},
		{"permission menu", &fakeExecutor{sessions: map[int]executor.Session{1: doneFor(long)}, sendErr: fmt.Errorf("orca: %w", executor.ErrBlocked)}, state.Entry{}},
		{"not accepted", &fakeExecutor{sessions: map[int]executor.Session{1: doneFor(long)}, sendErr: errors.New("did not accept")}, state.Entry{}},
		{"no live session", &fakeExecutor{}, state.Entry{}},
		{"still the same stop as the last send", &fakeExecutor{sessions: map[int]executor.Session{1: doneFor(long)}}, state.Entry{Continuations: 1, LastSentAt: now.Add(-long / 2)}},
		{"working", &fakeExecutor{sessions: map[int]executor.Session{1: {State: "working"}}}, state.Entry{}},
		{"two sessions or unknown state", &fakeExecutor{sessionErr: errors.New("2 live sessions")}, state.Entry{}},
	} {
		tg, _ := continueTarget(c.ex, working(1))
		hints := state.State{"o/r#1": c.hint}

		if err := Run(context.Background(), quiet, []Target{tg}, settings(hints)); err != nil {
			t.Errorf("%s: a skipped continuation is not a tick failure: %v", c.name, err)
		}

		if len(c.ex.sent) != 0 {
			t.Errorf("%s: sent = %q", c.name, c.ex.sent)
		}
		if hints["o/r#1"] != c.hint {
			t.Errorf("%s: hint = %+v, want unchanged %+v", c.name, hints["o/r#1"], c.hint)
		}
	}
}

func TestRunContinuingDoesNotUseADispatchSlot(t *testing.T) {
	ex := &fakeExecutor{sessions: map[int]executor.Session{1: doneFor(time.Hour)}}
	poll := working(1)
	poll.Candidates = []tracker.Issue{issue(2)}
	tg, tr := continueTarget(ex, poll)
	tg.MaxConcurrent = 2

	if err := Run(context.Background(), quiet, []Target{tg}, settings(nil)); err != nil {
		t.Fatal(err)
	}
	if len(ex.sent) != 1 || !slices.Equal(tr.moved, []int{2}) {
		t.Errorf("sent = %q, moved = %v; want #1 continued and #2 dispatched", ex.sent, tr.moved)
	}
}

func TestRunForgetsIssuesThatLeftWorking(t *testing.T) {
	a, _ := continueTarget(&fakeExecutor{}, working(1))
	b, _ := continueTarget(&fakeExecutor{}, working())
	hints := state.State{"o/r#1": {Continuations: 1}, "o/r#9": {Continuations: 3}}

	if err := Run(context.Background(), quiet, []Target{a, b}, settings(hints)); err != nil {
		t.Fatal(err)
	}
	if _, ok := hints["o/r#9"]; ok || len(hints) != 1 {
		t.Errorf("hints = %+v, want only o/r#1", hints)
	}
}

func TestRunKeepsHintsWhenAPollFails(t *testing.T) {
	broken := Target{Name: "broken", Tracker: &fakeTracker{pollErr: errors.New("gh is down")}, Executor: &fakeExecutor{}, MaxConcurrent: 1, Render: render}
	hints := state.State{"o/r#9": {Continuations: 3}}

	_ = Run(context.Background(), quiet, []Target{broken}, settings(hints))

	if _, ok := hints["o/r#9"]; !ok {
		t.Errorf("hints = %+v; a failed Poll must not forget the Issues it could not see", hints)
	}
}

func TestRunKeepsHintsWhenADispatchFailsOrAWorkflowIsMissing(t *testing.T) {
	failing := &fakeTracker{poll: tracker.Poll{Candidates: []tracker.Issue{issue(1)}}, missing: map[int]bool{1: true}}
	tg := Target{Name: "w", Tracker: failing, Executor: &fakeExecutor{}, MaxConcurrent: 1, Render: render}
	hints := state.State{"o/r#9": {Continuations: 3}}

	_ = Run(context.Background(), quiet, []Target{tg}, settings(hints))
	if _, ok := hints["o/r#9"]; ok {
		t.Errorf("a failed Dispatch must not stop forgetting Issues that left Working: %+v", hints)
	}

	hints = state.State{"o/r#9": {Continuations: 3}}
	c := settings(hints)
	c.Partial = true
	_ = Run(context.Background(), quiet, nil, c)
	if _, ok := hints["o/r#9"]; !ok {
		t.Errorf("hints = %+v; a Workflow that was not run must keep its Issues", hints)
	}
}
