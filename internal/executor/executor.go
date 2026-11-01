// Package executor は Dispatch された worker を起動する実行側のインターフェースを定める。
// 型は JSON に変換できるものに限る（ADR 0003: 後から外部コマンドの Adapter を足せるように）。
package executor

import "context"

// Issue は worker に渡す Issue の識別子。
type Issue struct {
	Repo   string `json:"repo"` // "owner/repo"
	Number int    `json:"number"`
}

type Request struct {
	Issue  Issue  `json:"issue"`
	Prompt string `json:"prompt"`
}

// Result は起動した worker の居場所。ログに残すためだけに使う。
type Result struct {
	Worktree string `json:"worktree"` // 作業場所のパス
	Session  string `json:"session"`  // Executor が返したセッションの識別子
}

type Executor interface {
	Dispatch(ctx context.Context, req Request) (Result, error)
}
