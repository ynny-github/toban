// Package workflow は Workflow ファイル（YAML の front matter と、プロンプトのテンプレートの本文）を読む。
package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ynny-github/toban/internal/executor/orca"
	"github.com/ynny-github/toban/internal/tracker"
	"github.com/ynny-github/toban/internal/tracker/github"
)

type Workflow struct {
	Path          string
	Tracker       TrackerConfig
	Executor      ExecutorConfig
	MaxConcurrent int
	prompt        *template.Template
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

type frontMatter struct {
	Tracker       TrackerConfig  `yaml:"tracker"`
	Executor      ExecutorConfig `yaml:"executor"`
	MaxConcurrent *int           `yaml:"max_concurrent"`
}

func Load(path string) (*Workflow, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	w, err := parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	w.Path = path
	return w, nil
}

func parse(src []byte) (*Workflow, error) {
	head, body, ok := strings.Cut(strings.TrimPrefix(string(src), "---\n"), "\n---\n")
	if !ok || !bytes.HasPrefix(src, []byte("---\n")) {
		return nil, fmt.Errorf("no YAML front matter between --- lines")
	}
	var fm frontMatter
	dec := yaml.NewDecoder(strings.NewReader(head))
	dec.KnownFields(true) // 知らないキーと、選べない kind のブロックはここでエラーになる
	if err := dec.Decode(&fm); err != nil {
		return nil, err
	}

	w := &Workflow{Tracker: fm.Tracker, Executor: fm.Executor, MaxConcurrent: 1}
	if fm.MaxConcurrent != nil {
		w.MaxConcurrent = *fm.MaxConcurrent
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

	t, err := template.New("prompt").Option("missingkey=error").Parse(body)
	if err != nil {
		return nil, fmt.Errorf("prompt template: %w", err)
	}
	w.prompt = t
	return w, nil
}

func setDefault(field *string, value string) {
	if *field == "" {
		*field = value
	}
}

// Render は Issue を渡してプロンプトのテンプレートを展開する。
func (w *Workflow) Render(issue tracker.Issue) (string, error) {
	var b strings.Builder
	if err := w.prompt.Execute(&b, struct{ Issue tracker.Issue }{issue}); err != nil {
		return "", fmt.Errorf("%s: prompt template: %w", w.Path, err)
	}
	return b.String(), nil
}

// LoadDir は dir の *.md をすべて名前順に読む。読めないファイルがあっても残りは返し、
// それぞれのエラーをまとめて返す（1 つの Workflow の誤りで、ほかを止めないため）。
func LoadDir(dir string) ([]*Workflow, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	var ws []*Workflow
	var errs []error
	for _, p := range paths {
		w, err := Load(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ws = append(ws, w)
	}
	return ws, errors.Join(errs...)
}

// Settings はユーザー単位の設定（~/.config/toban/config.yaml）。
type Settings struct {
	Interval time.Duration // tick の間隔
}

// LoadSettings は設定を読む。ファイルがなければ既定値を返す。
func LoadSettings(path string) (Settings, error) {
	s := Settings{Interval: 5 * time.Minute}
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var raw struct {
		Interval string `yaml:"interval"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Interval != "" {
		if s.Interval, err = time.ParseDuration(raw.Interval); err != nil {
			return s, fmt.Errorf("%s: interval: %w", path, err)
		}
	}
	if s.Interval <= 0 {
		return s, fmt.Errorf("%s: interval: must be positive, got %v", path, s.Interval)
	}
	return s, nil
}
