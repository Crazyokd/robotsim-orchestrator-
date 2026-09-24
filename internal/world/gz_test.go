package world

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitClockEnforcesItsOwnTimeout(t *testing.T) {
	gz := GZ{
		Run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	err := gz.waitClockWithTimeout(context.Background(), 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
}
