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

func TestLoadFillsDefaultsAndRendersPrompt(t *testing.T) {
	w, err := Load("testdata/workflows/toban.md")
	if err != nil {
		t.Fatal(err)
	}

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
	if w.MaxConcurrent != 2 {
		t.Errorf("max_concurrent = %d, want 2", w.MaxConcurrent)
	}

	got, err := w.Render(tracker.Issue{Repo: "ynny-github/toban", Number: 3, Title: "Workflow を読む", Body: "本文"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "ynny-github/toban#3 「Workflow を読む」に取り組む。\n\n本文\n"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
}

const validHead = `tracker:
  kind: github
  github:
    project: https://github.com/users/ynny-github/projects/3
    label: ready-for-agent
    assignee: ynny-github
executor:
  kind: orca
`

func writeWorkflow(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "w.md")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsBadWorkflows(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"unknown key", "---\n" + validHead + "  orca:\n    agnet: claude\n---\nbody\n", "agnet"},
		{"block of another kind", "---\n" + validHead + "  linear: {}\n---\nbody\n", "linear"},
		{"unknown tracker kind", "---\n" + strings.Replace(validHead, "kind: github", "kind: jira", 1) + "---\nbody\n", "tracker.kind"},
		{"unknown executor kind", "---\n" + strings.Replace(validHead, "kind: orca", "kind: herdr", 1) + "---\nbody\n", "executor.kind"},
		{"missing label", "---\n" + strings.Replace(validHead, "    label: ready-for-agent\n", "", 1) + "---\nbody\n", "tracker.github.label"},
		{"zero max_concurrent", "---\n" + validHead + "max_concurrent: 0\n---\nbody\n", "max_concurrent"},
		{"no front matter", "body only\n", "front matter"},
		{"bad template", "---\n" + validHead + "---\n{{.Issue.Number\n", "prompt template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWorkflow(t, tc.src)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
				t.Errorf("got error %v, want one naming %q and the file", err, tc.want)
			}
		})
	}
}

func TestLoadDirReadsEveryMarkdownFileInOrder(t *testing.T) {
	ws, err := LoadDir("testdata/workflows")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, w := range ws {
		paths = append(paths, w.Path)
	}
	want := []string{"testdata/workflows/another.md", "testdata/workflows/toban.md"} // README.txt は読まない
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
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

	if _, err := LoadSettings(writeWorkflow(t, "interval: 0s\n")); err == nil {
		t.Error("LoadSettings accepted a zero interval")
	}
}
