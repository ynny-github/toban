// Package tracker は Issue と State を持つ外部のシステムとのインターフェースを定める。
// 型は JSON に変換できるものに限る（ADR 0003: 後から外部コマンドの Adapter を足せるように）。
package tracker

import "context"

type Issue struct {
	ID     string   `json:"id"`   // Tracker の中での識別子（GitHub なら Project の item の ID）
	Repo   string   `json:"repo"` // "owner/repo"
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	URL    string   `json:"url"`
	Labels []string `json:"labels"`
	State  string   `json:"state"` // 取得したときの State
}

// Poll は 1 回の問い合わせで分かった、Dispatch の判断に要るもの。
type Poll struct {
	Candidates []Issue `json:"candidates"`
	Working    int     `json:"working"` // Working State にある Issue の数
}

type Tracker interface {
	Poll(ctx context.Context) (Poll, error)
	// MoveToWorking は Issue を Working State に移す。Poll のあとに呼ぶ。
	MoveToWorking(ctx context.Context, issue Issue) error
	// Restore は Issue を Poll で取得したときの State に戻す。
	Restore(ctx context.Context, issue Issue) error
	// Preamble は worker に渡すプロンプトの前置き（State の移し方など）を返す。
	Preamble(issue Issue) string
}
