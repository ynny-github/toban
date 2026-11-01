// Package github は GitHub Project の State で Issue を管理する Tracker。
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/ynny-github/toban/internal/tracker"
)

type Config struct {
	Project    string   `yaml:"project"`     // Project の URL
	StateField string   `yaml:"state_field"` // State を表す single select のフィールド名
	Ready      string   `yaml:"ready"`
	Working    string   `yaml:"working"`
	Label      string   `yaml:"label"`
	Assignee   string   `yaml:"assignee"`
	Repos      []string `yaml:"repos"` // 空ならすべてのリポジトリ
}

// Runner は gh コマンドを引数つきで実行し、標準出力を返す。
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Command は bin（gh の実行ファイル）を起動する Runner を返す。
// GraphQL のエラーでも応答は標準出力に出るので、エラーと一緒に返す。
func Command(bin string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, bin, args...).Output()
	}
}

type Tracker struct {
	cfg       Config
	run       Runner
	ownerType string // "user" か "organization"
	login     string
	number    int

	// Poll で分かった、State を書き換えるのに要る ID
	projectID string
	fieldID   string
	options   map[string]string // State の名前 → option の ID
	states    []string          // Project での State の並び順
}

func New(cfg Config, run Runner) (*Tracker, error) {
	t := &Tracker{cfg: cfg, run: run}
	u, err := url.Parse(cfg.Project)
	if err != nil {
		return nil, fmt.Errorf("github tracker: project %q: %w", cfg.Project, err)
	}
	// https://github.com/users/<login>/projects/<n> か https://github.com/orgs/<login>/projects/<n>
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Host != "github.com" || len(p) != 4 || p[2] != "projects" {
		return nil, fmt.Errorf("github tracker: project %q is not a GitHub Project URL", cfg.Project)
	}
	switch p[0] {
	case "users":
		t.ownerType = "user"
	case "orgs":
		t.ownerType = "organization"
	default:
		return nil, fmt.Errorf("github tracker: project %q is not a GitHub Project URL", cfg.Project)
	}
	t.login = p[1]
	if t.number, err = strconv.Atoi(p[3]); err != nil {
		return nil, fmt.Errorf("github tracker: project %q is not a GitHub Project URL", cfg.Project)
	}
	return t, nil
}

type name struct {
	Name string `json:"name"`
}

type item struct {
	ID      string `json:"id"`
	State   *name  `json:"state"`
	Content struct {
		Number     int    `json:"number"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		URL        string `json:"url"`
		State      string `json:"state"`
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
		Labels struct {
			Nodes []name `json:"nodes"`
		} `json:"labels"`
		Assignees struct {
			Nodes []struct {
				Login string `json:"login"`
			} `json:"nodes"`
		} `json:"assignees"`
	} `json:"content"`
}

func (t *Tracker) Poll(ctx context.Context) (tracker.Poll, error) {
	items, err := t.fetchItems(ctx)
	if err != nil {
		return tracker.Poll{}, err
	}
	var poll tracker.Poll
	for _, it := range items {
		c := it.Content
		if c.State != "OPEN" || it.State == nil { // closed は Terminal State。Issue 以外の item は content が空
			continue
		}
		switch it.State.Name {
		case t.cfg.Working:
			poll.Working++
		case t.cfg.Ready:
			if !t.isCandidate(it) {
				continue
			}
			issue := tracker.Issue{
				ID: it.ID, Repo: c.Repository.NameWithOwner, Number: c.Number,
				Title: c.Title, Body: c.Body, URL: c.URL, State: it.State.Name,
			}
			for _, l := range c.Labels.Nodes {
				issue.Labels = append(issue.Labels, l.Name)
			}
			poll.Candidates = append(poll.Candidates, issue)
		}
	}
	return poll, nil
}

// fetchItems は Project の item をすべてのページから集め、State の書き換えに要る ID も覚える。
func (t *Tracker) fetchItems(ctx context.Context) ([]item, error) {
	var items []item
	cursor := ""
	for {
		var data struct {
			Owner struct {
				ProjectV2 struct {
					ID    string `json:"id"`
					Field *struct {
						ID      string `json:"id"`
						Options []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"options"`
					} `json:"field"`
					Items struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []item `json:"nodes"`
					} `json:"items"`
				} `json:"projectV2"`
			} `json:"owner"`
		}
		args := []string{
			"-f", "query=" + fmt.Sprintf(itemsQuery, t.ownerType),
			"-f", "login=" + t.login,
			"-F", "number=" + strconv.Itoa(t.number),
			"-f", "field=" + t.cfg.StateField,
		}
		if cursor != "" {
			args = append(args, "-f", "cursor="+cursor)
		}
		if err := t.graphql(ctx, &data, args...); err != nil {
			return nil, err
		}
		p := data.Owner.ProjectV2
		if p.Field == nil || p.Field.ID == "" {
			return nil, fmt.Errorf("github tracker: %s has no single select field %q", t.cfg.Project, t.cfg.StateField)
		}
		t.projectID, t.fieldID, t.options, t.states = p.ID, p.Field.ID, map[string]string{}, nil
		for _, o := range p.Field.Options {
			t.options[o.Name] = o.ID
			t.states = append(t.states, o.Name)
		}
		items = append(items, p.Items.Nodes...)
		if !p.Items.PageInfo.HasNextPage {
			return items, nil
		}
		cursor = p.Items.PageInfo.EndCursor
	}
}

func (t *Tracker) isCandidate(it item) bool {
	c := it.Content
	if len(t.cfg.Repos) > 0 && !slices.Contains(t.cfg.Repos, c.Repository.NameWithOwner) {
		return false
	}
	hasLabel := slices.Contains(c.Labels.Nodes, name{t.cfg.Label})
	hasAssignee := slices.ContainsFunc(c.Assignees.Nodes, func(a struct {
		Login string `json:"login"`
	}) bool {
		return a.Login == t.cfg.Assignee
	})
	return hasLabel && hasAssignee
}

// graphql は `gh api graphql` を実行し、応答の data を out に読み込む。
// gh は GraphQL のエラーでも応答の JSON を標準出力に出し、終了コード 1 で終わる。
func (t *Tracker) graphql(ctx context.Context, out any, args ...string) error {
	stdout, runErr := t.run(ctx, append([]string{"api", "graphql"}, args...)...)
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout, &resp); err != nil {
		if runErr != nil {
			return fmt.Errorf("gh api graphql: %w", runErr)
		}
		return fmt.Errorf("gh api graphql: unreadable output: %w", err)
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("gh api graphql: %s", resp.Errors[0].Message)
	}
	return json.Unmarshal(resp.Data, out)
}

func (t *Tracker) MoveToWorking(ctx context.Context, issue tracker.Issue) error {
	return t.setState(ctx, issue, t.cfg.Working)
}

func (t *Tracker) Restore(ctx context.Context, issue tracker.Issue) error {
	return t.setState(ctx, issue, issue.State)
}

func (t *Tracker) setState(ctx context.Context, issue tracker.Issue, state string) error {
	option, ok := t.options[state]
	if !ok {
		return fmt.Errorf("github tracker: %s has no State %q in field %q (call Poll first)", t.cfg.Project, state, t.cfg.StateField)
	}
	var data struct{}
	if err := t.graphql(ctx, &data,
		"-f", "query="+setStateMutation,
		"-f", "project="+t.projectID,
		"-f", "item="+issue.ID,
		"-f", "field="+t.fieldID,
		"-f", "option="+option); err != nil {
		return fmt.Errorf("github tracker: move %s#%d to %q: %w", issue.Repo, issue.Number, state, err)
	}
	return nil
}

func (t *Tracker) Preamble(issue tracker.Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "この Issue（%s）の状態は、GitHub Project（%s）のフィールド「%s」で管理されている。", issue.URL, t.cfg.Project, t.cfg.StateField)
	fmt.Fprintf(&b, "toban がこの Issue を「%s」に移してから、あなたを起動した。\n\n", t.cfg.Working)
	b.WriteString("状態を移すときは、次のコマンドの <option-id> を下の一覧の ID に置き換えて実行する。\n\n")
	fmt.Fprintf(&b, "gh project item-edit --project-id %s --id %s --field-id %s --single-select-option-id <option-id>\n\n", t.projectID, issue.ID, t.fieldID)
	for _, s := range t.states {
		fmt.Fprintf(&b, "- %s: %s\n", s, t.options[s])
	}
	return b.String()
}
