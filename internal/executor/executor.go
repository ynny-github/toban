// Package executor は Dispatch された worker を起動する実行側のインターフェースを定める。
// 型は JSON に変換できるものに限る（ADR 0003: 後から外部コマンドの Adapter を足せるように）。
package executor

import (
	"context"
	"errors"
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

// Result は起動した worker の居場所。ログに残すためだけに使う。
type Result struct {
	Worktree string `json:"worktree"` // 作業場所のパス
	Session  string `json:"session"`  // Executor が返したセッションの識別子
}

type Executor interface {
	// Check は State を動かす前に呼ぶ。Dispatch できない状態ならエラーを返し、作業中なら ErrBusy を返す。
	Check(ctx context.Context, issue Issue) error
	Dispatch(ctx context.Context, req Request) (Result, error)
}
