package orca

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRateLimits(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{"account list": "account-list.json"}}
	got, err := New(Config{}, f.run).RateLimits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != 2 || got.Weekly != 6 || got.UpdatedAt.UnixMilli() != 1793715249408 {
		t.Errorf("got %+v", got)
	}
}

func TestRateLimitsUnavailable(t *testing.T) {
	f := &fakeOrca{t: t, responses: map[string]string{"account list": "account-list-unavailable.json"}}
	_, err := New(Config{}, f.run).RateLimits(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("err = %v, want one mentioning the cause", err)
	}
}

func TestOver(t *testing.T) {
	now := time.UnixMilli(1793715249408).Add(time.Minute)
	r := RateLimits{Session: 50, Weekly: 50, UpdatedAt: now.Add(-time.Minute)}
	for _, c := range []struct {
		name string
		r    RateLimits
		want string // 理由に含まれる語。空なら見送らない
	}{
		{"ok", r, ""},
		{"session at threshold", RateLimits{80, 50, r.UpdatedAt}, "5-hour"},
		{"weekly over", RateLimits{50, 95, r.UpdatedAt}, "weekly"},
		{"stale", RateLimits{1, 1, now.Add(-time.Hour)}, "stale"},
	} {
		got := c.r.Over(80, 90, 15*time.Minute, now)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: Over = %q, want containing %q", c.name, got, c.want)
		}
	}
}
