package github

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ynny-github/toban/internal/tracker"
)

// fakeGh は `gh api graphql` の呼び出しごとに testdata の JSON を順に返し、引数を記録する。
type fakeGh struct {
	t         *testing.T
	responses []string
	calls     [][]string
}

func (f *fakeGh) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(f.responses) == 0 {
		f.t.Fatalf("unexpected gh call: %q", args)
	}
	name := f.responses[0]
	f.responses = f.responses[1:]
	return os.ReadFile("testdata/" + name)
}

var project3 = Config{
	Project:    "https://github.com/users/ynny-github/projects/3",
	StateField: "Status",
	Ready:      "Todo",
	Working:    "In Progress",
	Label:      "ready-for-agent",
	Assignee:   "ynny-github",
}

func numbers(issues []tracker.Issue) []int {
	var ns []int
	for _, i := range issues {
		ns = append(ns, i.Number)
	}
	return ns
}

func TestPollFindsCandidatesAndCountsWorking(t *testing.T) {
	f := &fakeGh{t: t, responses: []string{"items.json"}}
	tr, err := New(project3, f.run)
	if err != nil {
		t.Fatal(err)
	}

	got, err := tr.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// #2 は In Progress、#6〜#8 はラベルが違う
	if want := []int{3, 4, 5}; !slices.Equal(numbers(got.Candidates), want) {
		t.Errorf("candidates = %v, want %v", numbers(got.Candidates), want)
	}
	if got.Working != 1 {
		t.Errorf("working = %d, want 1", got.Working)
	}
	c := got.Candidates[0]
	if c.ID != "PVTI_lAHOAouHos4BopILzhDZQyc" || c.Repo != "ynny-github/toban" || c.State != "Todo" ||
		c.URL != "https://github.com/ynny-github/toban/issues/3" {
		t.Errorf("candidate #3 = %+v", c)
	}
	args := strings.Join(f.calls[0], " ")
	for _, want := range []string{"owner: user(login: $login)", "login=ynny-github", "number=3", "field=Status"} {
		if !strings.Contains(args, want) {
			t.Errorf("gh args lack %q: %s", want, args)
		}
	}
}

// items-variants.json では #3 が closed、#4 に assignee がおらず、#5 が example/other のもの。
func TestPollSkipsClosedUnassignedAndOtherRepoIssues(t *testing.T) {
	cfg := project3
	cfg.Repos = []string{"ynny-github/toban"}
	f := &fakeGh{t: t, responses: []string{"items-variants.json"}}
	tr, _ := New(cfg, f.run)

	got, err := tr.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 0 {
		t.Errorf("candidates = %v, want none", numbers(got.Candidates))
	}
}

func TestPollReadsEveryPage(t *testing.T) {
	f := &fakeGh{t: t, responses: []string{"items-page1.json", "items-page2.json"}}
	tr, _ := New(project3, f.run)

	got, err := tr.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{3, 4, 5}; !slices.Equal(numbers(got.Candidates), want) {
		t.Errorf("candidates = %v, want %v", numbers(got.Candidates), want)
	}
	if len(f.calls) != 2 || !slices.Contains(f.calls[1], "cursor=CURSOR1") {
		t.Errorf("second page was not requested with the cursor: %q", f.calls)
	}
}

func TestPollReportsGraphQLError(t *testing.T) {
	f := &fakeGh{t: t, responses: []string{"items-noproject.json"}}
	tr, _ := New(project3, f.run)

	_, err := tr.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Could not resolve to a ProjectV2") {
		t.Errorf("got error %v, want the GraphQL message", err)
	}
}

func TestMoveToWorkingAndRestoreSetTheStateOption(t *testing.T) {
	f := &fakeGh{t: t, responses: []string{"items.json", "set-state.json", "set-state.json"}}
	tr, _ := New(project3, f.run)
	poll, err := tr.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	issue := poll.Candidates[0] // #3, Todo

	if err := tr.MoveToWorking(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	if err := tr.Restore(context.Background(), issue); err != nil {
		t.Fatal(err)
	}

	for i, option := range []string{"47fc9ee4", "f75ad846"} { // In Progress、Todo
		args := f.calls[i+1]
		for _, want := range []string{
			"project=PVT_kwHOAouHos4BopIL",
			"item=PVTI_lAHOAouHos4BopILzhDZQyc",
			"field=PVTSSF_lAHOAouHos4BopILzhnIQbU",
			"option=" + option,
		} {
			if !slices.Contains(args, want) {
				t.Errorf("call %d lacks %s: %q", i+1, want, args)
			}
		}
	}
}

func TestMoveToWorkingReportsGraphQLError(t *testing.T) {
	f := &fakeGh{t: t, responses: []string{"items.json", "set-state-error.json"}}
	tr, _ := New(project3, f.run)
	poll, _ := tr.Poll(context.Background())

	err := tr.MoveToWorking(context.Background(), poll.Candidates[0])
	if err == nil || !strings.Contains(err.Error(), "does not belong to the field") {
		t.Errorf("got error %v, want the GraphQL message", err)
	}
}

func TestMoveToWorkingFailsForUnknownState(t *testing.T) {
	cfg := project3
	cfg.Working = "Doing" // Project にない State
	f := &fakeGh{t: t, responses: []string{"items.json"}}
	tr, _ := New(cfg, f.run)
	poll, _ := tr.Poll(context.Background())

	if err := tr.MoveToWorking(context.Background(), poll.Candidates[0]); err == nil {
		t.Error("MoveToWorking succeeded with a State the Project does not have")
	}
}

// worker が自分で State を移せるように、そのまま実行できるコマンドと option の ID を渡す。
func TestPreambleTellsHowToMoveTheState(t *testing.T) {
	f := &fakeGh{t: t, responses: []string{"items.json"}}
	tr, _ := New(project3, f.run)
	poll, _ := tr.Poll(context.Background())

	got := tr.Preamble(poll.Candidates[0])

	for _, want := range []string{
		"https://github.com/ynny-github/toban/issues/3",
		"gh project item-edit --project-id PVT_kwHOAouHos4BopIL --id PVTI_lAHOAouHos4BopILzhDZQyc --field-id PVTSSF_lAHOAouHos4BopILzhnIQbU --single-select-option-id <option-id>",
		"- Todo: f75ad846",
		"- In Progress: 47fc9ee4",
		"- Done: 98236657",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preamble lacks %q:\n%s", want, got)
		}
	}
}
