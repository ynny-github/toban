// Package tick は toban tick の 1 周ぶんの処理（closed の Issue の作業場所を消し、Working State の Issue に継続を送るか起動し直し、Candidate を Dispatch する）を行う。
package tick

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ynny-github/toban/internal/executor"
	"github.com/ynny-github/toban/internal/state"
	"github.com/ynny-github/toban/internal/tracker"
)

// workflowFile は worker が従う手順書で、Dispatch の前に対象リポジトリにあることを確かめる（ADR 0005）。
const workflowFile = "WORKFLOW.md"

// Target は 1 つの Workflow を処理するのに要るもの。
type Target struct {
	Name          string
	Tracker       tracker.Tracker
	Executor      executor.Executor
	MaxConcurrent int
	Render        func(issue tracker.Issue, continuation bool) (string, error)
	// CloseAfter は、Issue が closed になってから作業場所を消すまでの時間。0 なら消さない（ADR 0009）
	CloseAfter time.Duration
}

// Continuation は、Working State の Issue のセッションが done のまま止まっているときや、working のまま動かないとき（停滞）、継続を送るための設定と記録。
// セッションが落ちていれば、継続の代わりに最初のプロンプトで起動し直す。
type Continuation struct {
	After      time.Duration // done に入ってからこの時間たったら送る
	StallAfter time.Duration // working のまま動きがなくこの時間たったら送る。0 なら送らない
	Max        int           // 1 つの Issue に送る回数の上限
	// RelaunchAfter は、落ちてから（最後に動いてから）起動し直すまでの時間。起動し直すたびに倍にする
	RelaunchAfter time.Duration
	MaxRelaunches int // 1 つの Issue を起動し直す回数の上限
	Now           time.Time
	Hints         state.State // 送った回数。Run が書き換え、Working State を離れた Issue のエントリを消す
	// Partial は、targets に入れられなかった Workflow があること。その Issue のエントリを消さないよう、片付けを見送る
	Partial bool
	// Over が空でなければ、使用量の上限を超えているか確かめられなかった理由。継続と Dispatch を見送る（作業場所の片付けはする）
	Over string
}

// Run はすべての Target を順に処理する。1 つの Target で失敗しても残りは続け、失敗をまとめて返す。
func Run(ctx context.Context, log *slog.Logger, targets []Target, c Continuation) error {
	var errs []error
	keep := map[string]bool{}
	allPolled := !c.Partial
	if c.Over != "" {
		log.Info("Claude usage is over the limit or unknown; not continuing or dispatching", "reason", c.Over)
	}
	for _, t := range targets {
		polled, err := runOne(ctx, log.With("workflow", t.Name), t, c, keep)
		allPolled = allPolled && polled
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
		}
	}
	// Poll できなかった Workflow の Issue まで消さないよう、すべて見えたときだけ片付ける
	if allPolled {
		c.Hints.Prune(keep)
	}
	return errors.Join(errs...)
}

// runOne は polled で Poll に成功したかどうかを返す。
func runOne(ctx context.Context, log *slog.Logger, t Target, c Continuation, keep map[string]bool) (polled bool, err error) {
	poll, err := t.Tracker.Poll(ctx)
	if err != nil {
		return false, err
	}
	free := max(0, t.MaxConcurrent-poll.Working)
	log.Info("polled", "candidates", len(poll.Candidates), "working", poll.Working, "free", free)

	// 使用量を使わないので、使用量の上限の手前で行う
	if t.CloseAfter > 0 {
		removeClosed(ctx, log, t, c.Now, poll.Closed)
	}

	// 継続は Dispatch の枠を使わず、失敗しても tick の失敗にしない（ログに残して次の周でやり直す）
	for _, issue := range poll.WorkingIssues {
		key := fmt.Sprintf("%s#%d", issue.Repo, issue.Number)
		keep[key] = true
		if c.Over == "" {
			continueOne(ctx, log.With("issue", key), t, c, key, issue)
		}
	}
	if c.Over != "" {
		return true, nil
	}

	// 起動前に弾いた Issue は枠を使わず、次の Candidate に進む。State を移したあとの失敗は
	// Tracker や Executor の不調の可能性が高いので、この周はそこで止める。
	var errs []error
	dispatched := 0
	for _, issue := range poll.Candidates {
		if dispatched >= free {
			break
		}
		stop, err := dispatch(ctx, log.With("issue", fmt.Sprintf("%s#%d", issue.Repo, issue.Number)), t, issue)
		if errors.Is(err, executor.ErrBusy) { // 失敗ではない。State は動かさず、次の Candidate に進む
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s#%d: %w", issue.Repo, issue.Number, err))
			if stop {
				break
			}
			continue
		}
		dispatched++
	}
	return true, errors.Join(errs...)
}

// dispatch は手順書の有無を確かめ、State を Working State に移してから worker を起動する。
// 起動に失敗したら State を戻す（ADR 0001）。stop は State を移したあとの失敗かどうか。
func dispatch(ctx context.Context, log *slog.Logger, t Target, issue tracker.Issue) (stop bool, err error) {
	if err := t.Tracker.CheckFile(ctx, issue, workflowFile); err != nil {
		log.Warn("skipped; State not moved", "error", err)
		return false, err
	}
	req, err := request(t, issue)
	if err != nil {
		return false, err
	}
	if err := t.Executor.Check(ctx, req.Issue); err != nil {
		if errors.Is(err, executor.ErrBusy) {
			log.Info("skipped; the session is still working; State not moved")
		}
		return false, err
	}
	if err := t.Tracker.MoveToWorking(ctx, issue); err != nil {
		return true, err
	}
	res, err := t.Executor.Dispatch(ctx, req)
	if err != nil {
		if rerr := t.Tracker.Restore(ctx, issue); rerr != nil {
			log.Error("launch failed and the State could not be restored; move it back by hand", "error", err, "restore_error", rerr)
			return true, errors.Join(err, rerr)
		}
		log.Warn("launch failed; State restored", "error", err)
		return true, err
	}
	log.Info("dispatched", "worktree", res.Worktree, "session", res.Session)
	return false, nil
}

// request は、最初のプロンプト（Preamble と本文）と続きのプロンプトを展開する。
func request(t Target, issue tracker.Issue) (executor.Request, error) {
	body, err := t.Render(issue, false)
	if err != nil {
		return executor.Request{}, err
	}
	continuation, err := t.Render(issue, true)
	if err != nil {
		return executor.Request{}, err
	}
	return executor.Request{
		Issue:              executor.Issue{Repo: issue.Repo, Number: issue.Number},
		Prompt:             strings.TrimRight(t.Tracker.Preamble(issue), "\n") + "\n\n" + body,
		ContinuationPrompt: continuation,
	}, nil
}

// continueOne は、Issue のセッションが done のまま c.After たっているか、working のまま動きがなく c.StallAfter たっていて、
// 上限に達していなければ、継続のプロンプトだけを送る。セッションが落ちていれば relaunchOne に任せる。State は動かさない。
func continueOne(ctx context.Context, log *slog.Logger, t Target, c Continuation, key string, issue tracker.Issue) {
	hint := c.Hints[key]
	eissue := executor.Issue{Repo: issue.Repo, Number: issue.Number}
	s, err := t.Executor.Session(ctx, eissue)
	if err != nil {
		log.Warn("cannot tell the session's state; not continuing", "error", err)
		return
	}
	if s.State == "dead" {
		relaunchOne(ctx, log, t, c, key, issue, s)
		return
	}
	if hint.Continuations >= c.Max {
		log.Info("continuation limit reached; not continuing", "continuations", hint.Continuations, "last_sent_at", hint.LastSentAt)
		return
	}
	wait := map[string]time.Duration{"done": c.After, "working": c.StallAfter}[s.State]
	// 前に送ったあと done に入り直すか動きがなければ、同じ停止に重ねて送らない
	if wait <= 0 || s.Since.IsZero() || c.Now.Sub(s.Since) < wait || !s.Since.After(hint.LastSentAt) {
		return
	}
	prompt, err := t.Render(issue, true)
	if err != nil {
		log.Warn("not continuing", "error", err)
		return
	}
	res, err := t.Executor.Continue(ctx, eissue, prompt)
	if errors.Is(err, executor.ErrBlocked) {
		log.Info("waiting for approval; not continuing", "error", err)
		return
	}
	if err != nil {
		log.Warn("continuation not sent", "error", err)
		return
	}
	hint.Continuations++
	hint.LastSentAt = c.Now
	c.Hints[key] = hint
	log.Info("continued", "continuations", hint.Continuations, "worktree", res.Worktree, "session", res.Session)
}

// removeClosed は、closed になってから t.CloseAfter たった Issue の作業場所を消す。
// 消せなかったら（コミット前の変更があるなど）ログに残し、次の周でまた試す。tick の失敗にはしない。
func removeClosed(ctx context.Context, log *slog.Logger, t Target, now time.Time, closed []tracker.Issue) {
	var due []executor.Issue
	for _, issue := range closed {
		if !issue.ClosedAt.IsZero() && now.Sub(issue.ClosedAt) >= t.CloseAfter {
			due = append(due, executor.Issue{Repo: issue.Repo, Number: issue.Number})
		}
	}
	removed, err := t.Executor.Remove(ctx, due)
	for _, path := range removed {
		log.Info("removed the worktree of a closed issue", "worktree", path)
	}
	if err != nil {
		log.Warn("could not remove the worktree of a closed issue; retrying next tick", "error", err)
	}
}

// relaunchOne は、落ちたセッションを最初のプロンプトで起動し直す（Symphony の「異常終了のあとの、バックオフ付きリトライ」）。
// 最後に動いたか、前に送ってから c.RelaunchAfter × 2^(起動し直した回数) たつまで待つ。State は動かさない。
func relaunchOne(ctx context.Context, log *slog.Logger, t Target, c Continuation, key string, issue tracker.Issue, s executor.Session) {
	hint := c.Hints[key]
	if hint.Relaunches >= c.MaxRelaunches {
		log.Info("relaunch limit reached; not relaunching", "relaunches", hint.Relaunches, "last_sent_at", hint.LastSentAt)
		return
	}
	// 落ちた時刻も前に送った時刻も分からなければ（どちらも空）、すぐ起動し直す。そのあとは LastSentAt から待つ
	since := s.Since
	if hint.LastSentAt.After(since) {
		since = hint.LastSentAt
	}
	if c.Now.Sub(since) < c.RelaunchAfter<<hint.Relaunches {
		return
	}
	req, err := request(t, issue)
	if err != nil {
		log.Warn("not relaunching", "error", err)
		return
	}
	res, err := t.Executor.Dispatch(ctx, req)
	if err != nil {
		log.Warn("relaunch failed", "error", err)
		return
	}
	hint.Relaunches++
	hint.LastSentAt = c.Now
	c.Hints[key] = hint
	log.Info("relaunched", "relaunches", hint.Relaunches, "worktree", res.Worktree, "session", res.Session)
}
