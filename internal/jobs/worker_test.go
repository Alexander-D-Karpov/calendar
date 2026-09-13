package jobs

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	for attempt := 0; attempt <= 40; attempt++ {
		d := backoff(attempt)
		if d < baseBackoff/2 || d > maxBackoff {
			t.Fatalf("backoff(%d) = %v", attempt, d)
		}
	}
	if backoff(1) > baseBackoff {
		t.Fatalf("first retry = %v", backoff(1))
	}
	if backoff(20) < maxBackoff/2 {
		t.Fatalf("late retry = %v", backoff(20))
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("bad payload")
	err := fmt.Errorf("sync: %w", Permanent(base))
	if !IsPermanent(err) || !errors.Is(err, base) || IsPermanent(base) || Permanent(nil) != nil {
		t.Fatal("permanent wrapping mismatch")
	}
	_ = time.Second
}
