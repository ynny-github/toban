package workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ynny-github/toban/internal/tracker"
	"github.com/ynny-github/toban/internal/tracker/github"
)

func TestLoadWorkflowsFillsDefaultsAndRendersPrompts(t *testing.T) {
	ws, err := LoadWorkflows("testdata/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].Name != "toban" || ws[1].Name != "another" {
		t.Fatalf("workflows = %+v, want toban then another", ws)
	}
	w := ws[0]

	wantGitHub := github.Config{
		Project:    "https://github.com/users/ynny-github/projects/3",
		StateField: "Status",
		Ready:      "Todo",
		Working:    "In Progress",
		Label:      "ready-for-agent",
		Assignee:   "ynny-github",
	}
	if w.Tracker.Kind != "github" || w.Tracker.GitHub == nil || !reflect.DeepEqual(*w.Tracker.GitHub, wantGitHub) {
		t.Errorf("tracker = %+v, want github %+v", w.Tracker, wantGitHub)
	}
	if w.Executor.Kind != "orca" || w.Executor.Orca == nil || w.Executor.Orca.Agent != "claude" {
		t.Errorf("executor = %+v, want orca with agent claude", w.Executor)
	}
	if w.MaxConcurrent != 2 || ws[1].MaxConcurrent != 1 {
		t.Errorf("max_concurrent = %d and %d, want 2 and 1", w.MaxConcurrent, ws[1].MaxConcurrent)
	}

	issue := tracker.Issue{Repo: "ynny-github/toban", Number: 3, Title: "Workflow を読む", URL: "https://example.com/3", Body: "本文"}
	first, err1 := w.Render(issue, false)
	again, err2 := w.Render(issue, true)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if want := "ynny-github/toban#3 「Workflow を読む」に取り組む。\n\n本文\n"; first != want {
		t.Errorf("prompt = %q, want %q", first, want)
	}
	if want := "続き https://example.com/3"; again != want {
		t.Errorf("continuation prompt = %q, want %q", again, want)
	}
}

const validWorkflow = `executor:
  kind: orca
trackers:
  - name: w
    kind: github
    github:
      project: https://github.com/users/ynny-github/projects/3
      label: ready-for-agent
      assignee: ynny-github
    prompt: p
    continuation_prompt: c
`

func writeConfig(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadWorkflowsRejectsBadWorkflows(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"unknown key", strings.Replace(validWorkflow, "  kind: orca\n", "  kind: orca\n  orca:\n    agnet: claude\n", 1), "agnet"},
		{"block of another kind", strings.Replace(validWorkflow, "    kind: github\n", "    kind: github\n    linear: {}\n", 1), "linear"},
		{"unknown tracker kind", strings.Replace(validWorkflow, "    kind: github", "    kind: jira", 1), "tracker.kind"},
		{"unknown executor kind", strings.Replace(validWorkflow, "executor:\n  kind: orca", "executor:\n  kind: herdr", 1), "executor.kind"},
		{"missing label", strings.Replace(validWorkflow, "      label: ready-for-agent\n", "", 1), "tracker.github.label"},
		{"missing name", strings.Replace(validWorkflow, "  - name: w\n    kind:", "  - kind:", 1), "name: required"},
		{"zero max_concurrent", validWorkflow + "    max_concurrent: 0\n", "max_concurrent"},
		{"missing prompt", strings.Replace(validWorkflow, "    prompt: p\n", "", 1), "prompt: required"},
		{"missing continuation_prompt", strings.Replace(validWorkflow, "    continuation_prompt: c\n", "", 1), "continuation_prompt: required"},
		{"bad template", strings.Replace(validWorkflow, "prompt: p", "prompt: '{{.Issue.Number'", 1), "prompt"},
		{"bad close_after", strings.Replace(validWorkflow, "  kind: orca\n", "  kind: orca\n  close_after: soon\n", 1), "executor.close_after"},
		{"negative close_after", strings.Replace(validWorkflow, "  kind: orca\n", "  kind: orca\n  close_after: -1m\n", 1), "executor.close_after"},
		{"duplicate name", validWorkflow + strings.SplitAfter(validWorkflow, "trackers:\n")[1], "used twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.src)
			_, err := LoadWorkflows(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
				t.Errorf("got error %v, want one naming %q and the file", err, tc.want)
			}
		})
	}
}

func TestLoadWorkflowsWithoutFileOrWorkflowsIsEmpty(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.yaml"), writeConfig(t, "interval: 1m\n")} {
		if ws, err := LoadWorkflows(path); err != nil || len(ws) != 0 {
			t.Errorf("LoadWorkflows(%s) = %v, %v, want nothing", path, ws, err)
		}
	}
}

func TestLoadSettings(t *testing.T) {
	s, err := LoadSettings("testdata/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if s.Interval != 2*time.Minute {
		t.Errorf("interval = %v, want 2m", s.Interval)
	}

	s, err = LoadSettings(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Interval != 5*time.Minute {
		t.Errorf("default interval = %v, want 5m", s.Interval)
	}
	if s.MaxSessionPercent != 80 || s.MaxWeeklyPercent != 90 {
		t.Errorf("default limits = %v/%v, want 80/90", s.MaxSessionPercent, s.MaxWeeklyPercent)
	}
	if s.ContinueAfter != 10*time.Minute || s.StallAfter != 30*time.Minute || s.MaxContinuations != 5 {
		t.Errorf("default continuation = %v/%v/%v, want 10m/30m/5", s.ContinueAfter, s.StallAfter, s.MaxContinuations)
	}
	if s.RelaunchAfter != 10*time.Minute || s.MaxRelaunches != 3 {
		t.Errorf("default relaunch = %v/%v, want 10m/3", s.RelaunchAfter, s.MaxRelaunches)
	}
	s, err = LoadSettings(writeConfig(t, "relaunch_after: 5m\nmax_relaunches: 0\n"))
	if err != nil || s.RelaunchAfter != 5*time.Minute || s.MaxRelaunches != 0 {
		t.Errorf("relaunch = %v/%v, %v; want 5m/0", s.RelaunchAfter, s.MaxRelaunches, err)
	}

	s, err = LoadSettings(writeConfig(t, "continue_after: 30m\nstall_after: 1h\nmax_continuations: 2\n"))
	if err != nil || s.ContinueAfter != 30*time.Minute || s.StallAfter != time.Hour || s.MaxContinuations != 2 {
		t.Errorf("continuation = %v/%v/%v, %v; want 30m/1h/2", s.ContinueAfter, s.StallAfter, s.MaxContinuations, err)
	}
	if s, err = LoadSettings(writeConfig(t, "stall_after: 0s\n")); err != nil || s.StallAfter != 0 {
		t.Errorf("stall_after: 0s = %v, %v; want 0 (off)", s.StallAfter, err)
	}
	for _, bad := range []string{"continue_after: 0s\n", "continue_after: -1m\n", "stall_after: -1m\n", "stall_after: soon\n", "max_continuations: 0\n", "relaunch_after: 0s\n", "relaunch_after: soon\n", "max_relaunches: -1\n"} {
		if _, err := LoadSettings(writeConfig(t, bad)); err == nil {
			t.Errorf("LoadSettings accepted %q", bad)
		}
	}

	s, err = LoadSettings(writeConfig(t, "max_weekly_percent: 70\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxSessionPercent != 80 || s.MaxWeeklyPercent != 70 {
		t.Errorf("limits = %v/%v, want 80/70", s.MaxSessionPercent, s.MaxWeeklyPercent)
	}
	if _, err := LoadSettings(writeConfig(t, "max_session_percent: 101\n")); err == nil {
		t.Error("LoadSettings accepted 101%")
	}

	if _, err := LoadSettings(writeConfig(t, "interval: 0s\n")); err == nil {
		t.Error("LoadSettings accepted a zero interval")
	}
}

func TestExecutorDefaultsToOrcaWithClaude(t *testing.T) {
	ws, err := LoadWorkflows(writeConfig(t, strings.Replace(validWorkflow, "executor:\n  kind: orca\n", "", 1)))
	if err != nil || len(ws) != 1 {
		t.Fatal(ws, err)
	}
	if e := ws[0].Executor; e.Kind != "orca" || e.Orca == nil || e.Orca.Agent != "claude" {
		t.Errorf("executor = %+v, want orca with agent claude", e)
	}
}

func TestCloseAfterIsOffUnlessSet(t *testing.T) {
	for src, want := range map[string]time.Duration{
		validWorkflow: 0,
		strings.Replace(validWorkflow, "  kind: orca\n", "  kind: orca\n  close_after: 0\n", 1):   0,
		strings.Replace(validWorkflow, "  kind: orca\n", "  kind: orca\n  close_after: 30m\n", 1): 30 * time.Minute,
	} {
		ws, err := LoadWorkflows(writeConfig(t, src))
		if err != nil || len(ws) != 1 {
			t.Fatal(ws, err)
		}
		if ws[0].CloseAfter != want {
			t.Errorf("close_after = %v, want %v", ws[0].CloseAfter, want)
		}
	}
}
