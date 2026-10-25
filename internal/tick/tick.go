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

// Target は 1 つの Workflow を処理するのに要るもの。
type Target struct {
	Name          string
	Tracker       tracker.Tracker
	Executor      executor.Executor
	MaxConcurrent int
	Render        func(tracker.Issue) (string, error)
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
	free := t.MaxConcurrent - poll.Working
	candidates := poll.Candidates[:max(0, min(free, len(poll.Candidates)))]
	log.Info("polled", "candidates", len(poll.Candidates), "working", poll.Working, "dispatching", len(candidates))

	var errs []error
	for _, issue := range candidates {
		if err := dispatch(ctx, log.With("issue", fmt.Sprintf("%s#%d", issue.Repo, issue.Number)), t, issue); err != nil {
			errs = append(errs, fmt.Errorf("%s#%d: %w", issue.Repo, issue.Number, err))
		}
	}
	return errors.Join(errs...)
}

// dispatch は State を Working State に移してから worker を起動する。起動に失敗したら State を戻す（ADR 0001）。
func dispatch(ctx context.Context, log *slog.Logger, t Target, issue tracker.Issue) error {
	body, err := t.Render(issue)
	if err != nil {
		return err
	}
	if err := t.Tracker.MoveToWorking(ctx, issue); err != nil {
		return err
	}
	res, err := t.Executor.Dispatch(ctx, executor.Request{
		Issue:  executor.Issue{Repo: issue.Repo, Number: issue.Number},
		Prompt: strings.TrimRight(t.Tracker.Preamble(issue), "\n") + "\n\n" + body,
	})
	if err != nil {
		if rerr := t.Tracker.Restore(ctx, issue); rerr != nil {
			log.Error("launch failed and the State could not be restored; move it back by hand", "error", err, "restore_error", rerr)
			return errors.Join(err, rerr)
		}
		log.Warn("launch failed; State restored", "error", err)
		return err
	}
	log.Info("dispatched", "worktree", res.Worktree, "session", res.Session)
	return nil
}
