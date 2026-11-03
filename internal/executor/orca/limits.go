package orca

import (
	"context"
	"fmt"
	"time"
)

// RateLimits は Claude のサブスクの利用率（%）。
type RateLimits struct {
	Session   float64 // 5 時間枠
	Weekly    float64 // 週次枠
	UpdatedAt time.Time
}

// RateLimits は `orca account list` が返す Claude の利用枠を読む。取れないときは理由つきのエラーにする。
func (e *Executor) RateLimits(ctx context.Context) (RateLimits, error) {
	var out struct {
		RateLimits struct {
			Claude *struct {
				Session   *struct{ UsedPercent float64 } `json:"session"`
				Weekly    *struct{ UsedPercent float64 } `json:"weekly"`
				UpdatedAt int64                          `json:"updatedAt"` // epoch ミリ秒
				Error     *string                        `json:"error"`
			} `json:"claude"`
		} `json:"rateLimits"`
	}
	if err := e.call(ctx, &out, "account", "list", "--json"); err != nil {
		return RateLimits{}, err
	}
	c := out.RateLimits.Claude
	switch {
	case c == nil:
		return RateLimits{}, fmt.Errorf("orca account list: no rateLimits.claude")
	case c.Error != nil:
		return RateLimits{}, fmt.Errorf("orca account list: claude rate limits: %s", *c.Error)
	case c.Session == nil || c.Weekly == nil:
		return RateLimits{}, fmt.Errorf("orca account list: claude rate limits lack session or weekly")
	}
	return RateLimits{c.Session.UsedPercent, c.Weekly.UsedPercent, time.UnixMilli(c.UpdatedAt)}, nil
}

// Over は新しい Dispatch を見送る理由を返す。問題なければ空文字列。
// 値が古いときも、枠の残りが分からないので見送る。
func (r RateLimits) Over(sessionMax, weeklyMax float64, staleAfter time.Duration, now time.Time) string {
	switch {
	case now.Sub(r.UpdatedAt) > staleAfter:
		return fmt.Sprintf("rate limits are stale (updated %s ago)", now.Sub(r.UpdatedAt).Round(time.Second))
	case r.Session >= sessionMax:
		return fmt.Sprintf("5-hour usage %.0f%% >= %.0f%%", r.Session, sessionMax)
	case r.Weekly >= weeklyMax:
		return fmt.Sprintf("weekly usage %.0f%% >= %.0f%%", r.Weekly, weeklyMax)
	}
	return ""
}
