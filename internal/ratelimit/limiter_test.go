package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterExpiresAndResets(t *testing.T) {
	l := New(10*time.Millisecond, 2, 4)
	if !l.Allow("a") || !l.Allow("a") || l.Allow("a") {
		t.Fatal("limit was not enforced")
	}
	l.Reset("a")
	if !l.Allow("a") {
		t.Fatal("reset did not release key")
	}
	time.Sleep(15 * time.Millisecond)
	if !l.Allow("a") {
		t.Fatal("expired key was not released")
	}
}
