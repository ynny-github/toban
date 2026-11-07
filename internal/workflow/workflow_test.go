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

const validWorkflow = `workflows:
  - name: w
    tracker:
      kind: github
      github:
        project: https://github.com/users/ynny-github/projects/3
        label: ready-for-agent
        assignee: ynny-github
    executor:
      kind: orca
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
		{"unknown key", strings.Replace(validWorkflow, "      kind: orca\n", "      kind: orca\n      orca:\n        agnet: claude\n", 1), "agnet"},
		{"block of another kind", strings.Replace(validWorkflow, "      kind: orca\n", "      kind: orca\n      linear: {}\n", 1), "linear"},
		{"unknown tracker kind", strings.Replace(validWorkflow, "kind: github", "kind: jira", 1), "tracker.kind"},
		{"unknown executor kind", strings.Replace(validWorkflow, "kind: orca", "kind: herdr", 1), "executor.kind"},
		{"missing label", strings.Replace(validWorkflow, "        label: ready-for-agent\n", "", 1), "tracker.github.label"},
		{"missing name", strings.Replace(validWorkflow, "  - name: w\n    tracker:", "  - tracker:", 1), "name: required"},
		{"zero max_concurrent", validWorkflow + "    max_concurrent: 0\n", "max_concurrent"},
		{"missing prompt", strings.Replace(validWorkflow, "    prompt: p\n", "", 1), "prompt: required"},
		{"missing continuation_prompt", strings.Replace(validWorkflow, "    continuation_prompt: c\n", "", 1), "continuation_prompt: required"},
		{"bad template", strings.Replace(validWorkflow, "prompt: p", "prompt: '{{.Issue.Number'", 1), "prompt"},
		{"duplicate name", validWorkflow + strings.TrimPrefix(validWorkflow, "workflows:\n"), "used twice"},
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
