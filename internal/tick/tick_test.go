package tick

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
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
	removeErr  error
	removeArgs [][]int // Remove に渡された Issue の番号（呼ばれるたびに 1 件）
}

func (f *fakeExecutor) Remove(_ context.Context, issues []executor.Issue) ([]string, error) {
	var ns []int
	var paths []string
	for _, i := range issues {
		ns = append(ns, i.Number)
		paths = append(paths, fmt.Sprintf("/wt/%d", i.Number))
	}
	f.removeArgs = append(f.removeArgs, ns)
	if f.removeErr != nil {
		return nil, f.removeErr
	}
	return paths, nil
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
	return Continuation{After: 10 * time.Minute, StallAfter: 30 * time.Minute, Max: 5, Now: now, Hints: hints}
}

func doneFor(d time.Duration) executor.Session {
	return executor.Session{State: "done", Since: now.Add(-d)}
}

// stalledFor は、working のまま d のあいだ動きがないセッション。
func stalledFor(d time.Duration) executor.Session {
	return executor.Session{State: "working", Since: now.Add(-d)}
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
		{"working and moving", &fakeExecutor{sessions: map[int]executor.Session{1: stalledFor(29 * time.Minute)}}, state.Entry{}},
		{"working with no activity time", &fakeExecutor{sessions: map[int]executor.Session{1: {State: "working"}}}, state.Entry{}},
		{"stalled but limit reached", &fakeExecutor{sessions: map[int]executor.Session{1: stalledFor(time.Hour)}}, state.Entry{Continuations: 5}},
		{"stalled but waiting for approval", &fakeExecutor{sessions: map[int]executor.Session{1: stalledFor(time.Hour)}, sendErr: fmt.Errorf("orca: %w", executor.ErrBlocked)}, state.Entry{}},
		{"still the same stall as the last send", &fakeExecutor{sessions: map[int]executor.Session{1: stalledFor(time.Hour)}}, state.Entry{Continuations: 1, LastSentAt: now.Add(-time.Hour / 2)}},
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

func TestRunContinuesAStalledSession(t *testing.T) {
	ex := &fakeExecutor{sessions: map[int]executor.Session{1: stalledFor(30 * time.Minute), 2: stalledFor(29 * time.Minute)}}
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
	if len(tr.moved) != 0 || len(tr.restored) != 0 || len(ex.reqs) != 0 {
		t.Errorf("moved = %v, restored = %v, dispatched = %d; want the State untouched", tr.moved, tr.restored, len(ex.reqs))
	}
}

func TestRunDoesNotContinueStalledSessionsWhenStallAfterIsZero(t *testing.T) {
	ex := &fakeExecutor{sessions: map[int]executor.Session{1: stalledFor(24 * time.Hour), 2: doneFor(time.Hour)}}
	tg, _ := continueTarget(ex, working(1, 2))
	c := settings(nil)
	c.StallAfter = 0

	if err := Run(context.Background(), quiet, []Target{tg}, c); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(ex.sent, []string{"2 CONTINUE"}) {
		t.Errorf("sent = %q, want only the continuation to the done #2", ex.sent)
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

func closedFor(n int, d time.Duration) tracker.Issue {
	i := issue(n)
	i.ClosedAt = now.Add(-d)
	return i
}

// 再オープンした Issue は open に戻り、Tracker が Closed に入れないので、ここには来ない。
func TestRunRemovesWorktreesOfIssuesClosedLongEnough(t *testing.T) {
	ex := &fakeExecutor{}
	tr := &fakeTracker{poll: tracker.Poll{Closed: []tracker.Issue{closedFor(1, 31*time.Minute), closedFor(2, 29*time.Minute), issue(3)}}}

	err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render, CloseAfter: 30 * time.Minute}}, settings(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.removeArgs) != 1 || !slices.Equal(ex.removeArgs[0], []int{1}) {
		t.Errorf("Remove got %v, want [[1]] (#2 is within close_after, #3 has no closedAt)", ex.removeArgs)
	}
}

func TestRunDoesNotRemoveWhenCloseAfterIsZero(t *testing.T) {
	ex := &fakeExecutor{}
	tr := &fakeTracker{poll: tracker.Poll{Closed: []tracker.Issue{closedFor(1, 24*time.Hour)}}}

	if err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render}}, settings(nil)); err != nil {
		t.Fatal(err)
	}
	if len(ex.removeArgs) != 0 {
		t.Errorf("Remove got %v, want no call", ex.removeArgs)
	}
}

func TestRunLogsAFailedRemovalWithoutFailing(t *testing.T) {
	ex := &fakeExecutor{removeErr: errors.New("/wt/1: contains modified or untracked files")}
	tr := &fakeTracker{poll: tracker.Poll{Closed: []tracker.Issue{closedFor(1, time.Hour)}}}
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))

	if err := Run(context.Background(), log, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 1, Render: render, CloseAfter: 30 * time.Minute}}, settings(nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "untracked files") {
		t.Errorf("log = %q, want a warning with the reason", buf.String())
	}
}

// 使用量の上限を超えていても片付けはするが、継続と Dispatch は見送り、送った回数の記録は残す。
func TestRunRemovesButDoesNotDispatchOrContinueWhenOverTheLimit(t *testing.T) {
	ex := &fakeExecutor{sessions: map[int]executor.Session{2: doneFor(time.Hour)}}
	poll := tracker.Poll{Candidates: []tracker.Issue{issue(1)}, WorkingIssues: []tracker.Issue{issue(2)}, Working: 1, Closed: []tracker.Issue{closedFor(3, time.Hour)}}
	tr := &fakeTracker{poll: poll}
	hints := state.State{"o/r#2": {Continuations: 1}}
	c := settings(hints)
	c.Over = "session 85% >= 80%"

	if err := Run(context.Background(), quiet, []Target{{Name: "w", Tracker: tr, Executor: ex, MaxConcurrent: 2, Render: render, CloseAfter: 30 * time.Minute}}, c); err != nil {
		t.Fatal(err)
	}
	if len(ex.removeArgs) != 1 || len(tr.moved) != 0 || len(ex.sent) != 0 {
		t.Errorf("removed %v, moved %v, sent %v; want only the removal", ex.removeArgs, tr.moved, ex.sent)
	}
	if hints["o/r#2"].Continuations != 1 {
		t.Errorf("hints = %v, want o/r#2 kept", hints)
	}
}
