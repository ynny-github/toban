// Package workflow は設定のファイル（config.yaml）を読む。ユーザー単位の設定と、Project ごとの Workflow が入っている。
package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ynny-github/toban/internal/executor/orca"
	"github.com/ynny-github/toban/internal/tracker"
	"github.com/ynny-github/toban/internal/tracker/github"
)

type Workflow struct {
	Name          string
	Tracker       TrackerConfig
	Executor      ExecutorConfig
	MaxConcurrent int
	prompt        *template.Template
	continuation  *template.Template
}

// TrackerConfig は kind で選んだ Tracker と、kind と同じ名前のブロックの設定。
type TrackerConfig struct {
	Kind   string         `yaml:"kind"`
	GitHub *github.Config `yaml:"github"`
}

type ExecutorConfig struct {
	Kind string       `yaml:"kind"`
	Orca *orca.Config `yaml:"orca"`
}

// file は config.yaml の全体。知らないキーはエラーにする。
type file struct {
	Interval          string     `yaml:"interval"`
	MaxSessionPercent *float64   `yaml:"max_session_percent"`
	MaxWeeklyPercent  *float64   `yaml:"max_weekly_percent"`
	Workflows         []workflow `yaml:"workflows"`
}

type workflow struct {
	Name               string         `yaml:"name"`
	Tracker            TrackerConfig  `yaml:"tracker"`
	Executor           ExecutorConfig `yaml:"executor"`
	MaxConcurrent      *int           `yaml:"max_concurrent"`
	Prompt             string         `yaml:"prompt"`
	ContinuationPrompt string         `yaml:"continuation_prompt"`
}

// read は config.yaml を読む。ファイルがなければ空として扱う。
func read(path string) (file, error) {
	var f file
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true) // 知らないキーと、選べない kind のブロックはここでエラーになる
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// LoadWorkflows は config.yaml の workflows を読む。1 つでも誤りがあれば、どれも返さずエラーにする。
func LoadWorkflows(path string) ([]*Workflow, error) {
	f, err := read(path)
	if err != nil {
		return nil, err
	}
	var ws []*Workflow
	seen := map[string]bool{}
	for i, raw := range f.Workflows {
		w, err := build(raw)
		if err == nil && seen[w.Name] {
			err = fmt.Errorf("name: %q is used twice", w.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: workflows[%d]: %w", path, i, err)
		}
		seen[w.Name] = true
		ws = append(ws, w)
	}
	return ws, nil
}

func build(raw workflow) (*Workflow, error) {
	if raw.Name == "" {
		return nil, fmt.Errorf("name: required")
	}
	w := &Workflow{Name: raw.Name, Tracker: raw.Tracker, Executor: raw.Executor, MaxConcurrent: 1}
	if raw.MaxConcurrent != nil {
		w.MaxConcurrent = *raw.MaxConcurrent
	}
	if w.MaxConcurrent < 1 {
		return nil, fmt.Errorf("max_concurrent: must be 1 or more, got %d", w.MaxConcurrent)
	}

	switch w.Tracker.Kind {
	case "github":
		if w.Tracker.GitHub == nil {
			w.Tracker.GitHub = &github.Config{}
		}
		g := w.Tracker.GitHub
		for key, v := range map[string]string{"project": g.Project, "label": g.Label, "assignee": g.Assignee} {
			if v == "" {
				return nil, fmt.Errorf("tracker.github.%s: required", key)
			}
		}
		setDefault(&g.StateField, "Status")
		setDefault(&g.Ready, "Todo")
		setDefault(&g.Working, "In Progress")
	default:
		return nil, fmt.Errorf("tracker.kind: unknown kind %q (known: github)", w.Tracker.Kind)
	}
	switch w.Executor.Kind {
	case "orca":
		if w.Executor.Orca == nil {
			w.Executor.Orca = &orca.Config{}
		}
		setDefault(&w.Executor.Orca.Agent, "claude")
	default:
		return nil, fmt.Errorf("executor.kind: unknown kind %q (known: orca)", w.Executor.Kind)
	}

	var err error
	if w.prompt, err = parseTemplate("prompt", raw.Prompt); err != nil {
		return nil, err
	}
	if w.continuation, err = parseTemplate("continuation_prompt", raw.ContinuationPrompt); err != nil {
		return nil, err
	}
	return w, nil
}

func parseTemplate(key, text string) (*template.Template, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("%s: required", key)
	}
	t, err := template.New(key).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return t, nil
}

func setDefault(field *string, value string) {
	if *field == "" {
		*field = value
	}
}

// Render は Issue を渡して、最初のプロンプトを展開する。
// continuation が真のときは、生きているセッションへ送る続きの指示を展開する（ADR 0006）。
func (w *Workflow) Render(issue tracker.Issue, continuation bool) (string, error) {
	t := w.prompt
	if continuation {
		t = w.continuation
	}
	var b strings.Builder
	if err := t.Execute(&b, struct{ Issue tracker.Issue }{issue}); err != nil {
		return "", fmt.Errorf("%s: %s: %w", w.Name, t.Name(), err)
	}
	return b.String(), nil
}

// Settings はユーザー単位の設定（config.yaml の workflows 以外）。
type Settings struct {
	Interval time.Duration // tick の間隔
	// この利用率（%）以上なら、新しい Dispatch を見送る
	MaxSessionPercent float64
	MaxWeeklyPercent  float64
}

// LoadSettings は設定を読む。ファイルがなければ既定値を返す。
func LoadSettings(path string) (Settings, error) {
	s := Settings{Interval: 5 * time.Minute, MaxSessionPercent: 80, MaxWeeklyPercent: 90}
	raw, err := read(path)
	if err != nil {
		return s, err
	}
	if raw.Interval != "" {
		if s.Interval, err = time.ParseDuration(raw.Interval); err != nil {
			return s, fmt.Errorf("%s: interval: %w", path, err)
		}
	}
	for key, p := range map[string]struct {
		in  *float64
		out *float64
	}{
		"max_session_percent": {raw.MaxSessionPercent, &s.MaxSessionPercent},
		"max_weekly_percent":  {raw.MaxWeeklyPercent, &s.MaxWeeklyPercent},
	} {
		if p.in == nil {
			continue
		}
		if *p.in <= 0 || *p.in > 100 {
			return s, fmt.Errorf("%s: %s: must be in (0, 100], got %v", path, key, *p.in)
		}
		*p.out = *p.in
	}
	if s.Interval <= 0 {
		return s, fmt.Errorf("%s: interval: must be positive, got %v", path, s.Interval)
	}
	return s, nil
}
