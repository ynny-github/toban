// Package executor は Dispatch された worker を起動する実行側のインターフェースを定める。
// 型は JSON に変換できるものに限る（ADR 0003: 後から外部コマンドの Adapter を足せるように）。
package executor

import (
	"context"
	"errors"
	"time"
)

// Issue は worker に渡す Issue の識別子。
type Issue struct {
	Repo   string `json:"repo"` // "owner/repo"
	Number int    `json:"number"`
}

type Request struct {
	Issue  Issue  `json:"issue"`
	Prompt string `json:"prompt"`
	// ContinuationPrompt は、Issue のセッションがすでに生きているとき、そこへ送る続きの指示（ADR 0006）。
	ContinuationPrompt string `json:"continuation_prompt"`
}

// ErrBusy は、Issue のセッションがまだ作業中なので、今は Dispatch しないことを表す。失敗ではない。
var ErrBusy = errors.New("the worker is still working")

// ErrBlocked は、セッションが許可メニューを出していて（承認待ち）、送った指示を受け付けなかったことを表す。
var ErrBlocked = errors.New("the agent is waiting for approval")

// Session は Issue の作業場所で生きているセッションの様子。
type Session struct {
	State string    `json:"state"` // "done"（返答を終えて止まっている）、"working"、""（生きたセッションがない）
	Since time.Time `json:"since"` // "done" なら done に入った時刻、"working" なら最後に動いた時刻（分からなければ空）
}

// Result は起動した worker の居場所。ログに残すためだけに使う。
type Result struct {
	Worktree string `json:"worktree"` // 作業場所のパス
	Session  string `json:"session"`  // Executor が返したセッションの識別子
}

type Executor interface {
	// Check は State を動かす前に呼ぶ。Dispatch できない状態ならエラーを返し、作業中なら ErrBusy を返す。
	Check(ctx context.Context, issue Issue) error
	Dispatch(ctx context.Context, req Request) (Result, error)
	// Session は Issue のセッションの様子を返す。どれに送るか決められないときはエラーを返す。
	Session(ctx context.Context, issue Issue) (Session, error)
	// Continue は生きたセッション（"done" か "working"）に prompt だけを送る。新しく起動はしない。
	Continue(ctx context.Context, issue Issue, prompt string) (Result, error)
	// Remove は closed になった issues の作業場所をすべて消す。作業中のセッションがあるものは残す。
	// removed は消した作業場所のパス。消せなかったものは、残りを続けたうえでエラーにまとめて返す。
	Remove(ctx context.Context, issues []Issue) (removed []string, err error)
}
