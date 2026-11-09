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
	// CloseAfter は、Issue が closed になってから作業場所を消すまでの時間。0 なら消さない（ADR 0009）
	CloseAfter   time.Duration
	prompt       *template.Template
	continuation *template.Template
}

// TrackerConfig は kind で選んだ Tracker と、kind と同じ名前のブロックの設定。
type TrackerConfig struct {
	Kind   string         `yaml:"kind"`
	GitHub *github.Config `yaml:"github"`
}

type ExecutorConfig struct {
	Kind string       `yaml:"kind"`
	Orca *orca.Config `yaml:"orca"`
	// CloseAfter は書かれたままの値。解釈した値は Workflow.CloseAfter に入る
	CloseAfter string `yaml:"close_after"`
}

// file は config.yaml の全体。知らないキーはエラーにする。
type file struct {
	Interval          string         `yaml:"interval"`
	MaxSessionPercent *float64       `yaml:"max_session_percent"`
	MaxWeeklyPercent  *float64       `yaml:"max_weekly_percent"`
	ContinueAfter     string         `yaml:"continue_after"`
	StallAfter        string         `yaml:"stall_after"`
	MaxContinuations  *int           `yaml:"max_continuations"`
	RelaunchAfter     string         `yaml:"relaunch_after"`
	MaxRelaunches     *int           `yaml:"max_relaunches"`
	Executor          ExecutorConfig `yaml:"executor"`
	Trackers          []trackerEntry `yaml:"trackers"`
}

// trackerEntry は Tracker の管理単位（GitHub なら Project）1 つ分。
type trackerEntry struct {
	Name               string `yaml:"name"`
	TrackerConfig      `yaml:",inline"`
	MaxConcurrent      *int   `yaml:"max_concurrent"`
	Prompt             string `yaml:"prompt"`
	ContinuationPrompt string `yaml:"continuation_prompt"`
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

// LoadWorkflows は config.yaml の trackers を読み、root の executor と組み合わせて Workflow にする。1 つでも誤りがあれば、どれも返さずエラーにする。
func LoadWorkflows(path string) ([]*Workflow, error) {
	f, err := read(path)
	if err != nil {
		return nil, err
	}
	exec, err := buildExecutor(f.Executor)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var closeAfter time.Duration
	if exec.CloseAfter != "" {
		if closeAfter, err = time.ParseDuration(exec.CloseAfter); err != nil {
			return nil, fmt.Errorf("%s: executor.close_after: %w", path, err)
		}
		if closeAfter < 0 {
			return nil, fmt.Errorf("%s: executor.close_after: must be 0 (off) or positive, got %v", path, closeAfter)
		}
	}
	var ws []*Workflow
	seen := map[string]bool{}
	for i, raw := range f.Trackers {
		w, err := build(raw, exec)
		if err == nil && seen[w.Name] {
			err = fmt.Errorf("name: %q is used twice", w.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: trackers[%d]: %w", path, i, err)
		}
		seen[w.Name] = true
		w.CloseAfter = closeAfter
		ws = append(ws, w)
	}
	return ws, nil
}

func build(raw trackerEntry, exec ExecutorConfig) (*Workflow, error) {
	if raw.Name == "" {
		return nil, fmt.Errorf("name: required")
	}
	w := &Workflow{Name: raw.Name, Tracker: raw.TrackerConfig, Executor: exec, MaxConcurrent: 1}
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
	var err error
	if w.prompt, err = parseTemplate("prompt", raw.Prompt); err != nil {
		return nil, err
	}
	if w.continuation, err = parseTemplate("continuation_prompt", raw.ContinuationPrompt); err != nil {
		return nil, err
	}
	return w, nil
}

// buildExecutor は root の executor に既定値を入れる。省略すると orca（agent は claude）になる。
func buildExecutor(e ExecutorConfig) (ExecutorConfig, error) {
	setDefault(&e.Kind, "orca")
	switch e.Kind {
	case "orca":
		if e.Orca == nil {
			e.Orca = &orca.Config{}
		}
		setDefault(&e.Orca.Agent, "claude")
	default:
		return e, fmt.Errorf("executor.kind: unknown kind %q (known: orca)", e.Kind)
	}
	return e, nil
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

// Settings はユーザー単位の設定（config.yaml の executor と trackers 以外）。
type Settings struct {
	Interval time.Duration // tick の間隔
	// この利用率（%）以上なら、新しい Dispatch を見送る
	MaxSessionPercent float64
	MaxWeeklyPercent  float64
	// Working State の Issue のセッションが done のままこの時間たったら、継続を送る
	ContinueAfter time.Duration
	// Working State の Issue のセッションが working のまま動きがなくこの時間たったら、継続を送る。0 なら送らない
	StallAfter time.Duration
	// 1 つの Issue に継続を送る回数の上限
	MaxContinuations int
	// Working State の Issue のセッションが落ちてからこの時間たったら、最初のプロンプトで起動し直す。起動し直すたびに倍にする
	RelaunchAfter time.Duration
	// 1 つの Issue を起動し直す回数の上限
	MaxRelaunches int
}

// LoadSettings は設定を読む。ファイルがなければ既定値を返す。
func LoadSettings(path string) (Settings, error) {
	s := Settings{Interval: 5 * time.Minute, MaxSessionPercent: 80, MaxWeeklyPercent: 90, ContinueAfter: 10 * time.Minute, StallAfter: 30 * time.Minute, MaxContinuations: 5, RelaunchAfter: 10 * time.Minute, MaxRelaunches: 3}
	raw, err := read(path)
	if err != nil {
		return s, err
	}
	if raw.Interval != "" {
		if s.Interval, err = time.ParseDuration(raw.Interval); err != nil {
			return s, fmt.Errorf("%s: interval: %w", path, err)
		}
	}
	if raw.ContinueAfter != "" {
		if s.ContinueAfter, err = time.ParseDuration(raw.ContinueAfter); err != nil {
			return s, fmt.Errorf("%s: continue_after: %w", path, err)
		}
		if s.ContinueAfter <= 0 {
			return s, fmt.Errorf("%s: continue_after: must be positive, got %v", path, s.ContinueAfter)
		}
	}
	if raw.StallAfter != "" {
		if s.StallAfter, err = time.ParseDuration(raw.StallAfter); err != nil {
			return s, fmt.Errorf("%s: stall_after: %w", path, err)
		}
		if s.StallAfter < 0 {
			return s, fmt.Errorf("%s: stall_after: must be 0 (off) or positive, got %v", path, s.StallAfter)
		}
	}
	if raw.MaxContinuations != nil {
		if *raw.MaxContinuations <= 0 {
			return s, fmt.Errorf("%s: max_continuations: must be 1 or more, got %d", path, *raw.MaxContinuations)
		}
		s.MaxContinuations = *raw.MaxContinuations
	}
	if raw.RelaunchAfter != "" {
		if s.RelaunchAfter, err = time.ParseDuration(raw.RelaunchAfter); err != nil {
			return s, fmt.Errorf("%s: relaunch_after: %w", path, err)
		}
		if s.RelaunchAfter <= 0 {
			return s, fmt.Errorf("%s: relaunch_after: must be positive, got %v", path, s.RelaunchAfter)
		}
	}
	if raw.MaxRelaunches != nil {
		if *raw.MaxRelaunches < 0 {
			return s, fmt.Errorf("%s: max_relaunches: must be 0 (off) or more, got %d", path, *raw.MaxRelaunches)
		}
		s.MaxRelaunches = *raw.MaxRelaunches
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
