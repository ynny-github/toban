// Package tick は toban tick の 1 周ぶんの処理（Candidate を Dispatch する）を行う。
package tick

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ynny-github/toban/internal/executor"
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
}

// Run はすべての Target を順に処理する。1 つの Target で失敗しても残りは続け、失敗をまとめて返す。
func Run(ctx context.Context, log *slog.Logger, targets []Target) error {
	var errs []error
	for _, t := range targets {
		if err := runOne(ctx, log.With("workflow", t.Name), t); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
		}
	}
	return errors.Join(errs...)
}

func runOne(ctx context.Context, log *slog.Logger, t Target) error {
	poll, err := t.Tracker.Poll(ctx)
	if err != nil {
		return err
	}
	free := max(0, t.MaxConcurrent-poll.Working)
	log.Info("polled", "candidates", len(poll.Candidates), "working", poll.Working, "free", free)

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
	return errors.Join(errs...)
}

// dispatch は手順書の有無を確かめ、State を Working State に移してから worker を起動する。
// 起動に失敗したら State を戻す（ADR 0001）。stop は State を移したあとの失敗かどうか。
func dispatch(ctx context.Context, log *slog.Logger, t Target, issue tracker.Issue) (stop bool, err error) {
	if err := t.Tracker.CheckFile(ctx, issue, workflowFile); err != nil {
		log.Warn("skipped; State not moved", "error", err)
		return false, err
	}
	body, err := t.Render(issue, false)
	if err != nil {
		return false, err
	}
	continuation, err := t.Render(issue, true)
	if err != nil {
		return false, err
	}
	eissue := executor.Issue{Repo: issue.Repo, Number: issue.Number}
	if err := t.Executor.Check(ctx, eissue); err != nil {
		if errors.Is(err, executor.ErrBusy) {
			log.Info("skipped; the session is still working; State not moved")
		}
		return false, err
	}
	if err := t.Tracker.MoveToWorking(ctx, issue); err != nil {
		return true, err
	}
	res, err := t.Executor.Dispatch(ctx, executor.Request{
		Issue:              eissue,
		Prompt:             strings.TrimRight(t.Tracker.Preamble(issue), "\n") + "\n\n" + body,
		ContinuationPrompt: continuation,
	})
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
