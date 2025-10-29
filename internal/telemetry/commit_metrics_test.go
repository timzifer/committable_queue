package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultCommitMetricsSingleton(t *testing.T) {
	if DefaultCommitMetrics() != DefaultCommitMetrics() {
		t.Fatalf("expected default metrics to return singleton instance")
	}
}

func TestTraceCommitRecordsAttemptsFailuresAndDuration(t *testing.T) {
	metrics := DefaultCommitMetrics()
	metrics.Reset()

	ctx := context.Background()

	ctx, finish := TraceCommit(ctx)
	time.Sleep(time.Millisecond)
	finish(nil)

	_, finish = TraceCommit(ctx)
	finish(errors.New("commit failed"))

	attempts, failures, average := metrics.Snapshot()
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if failures != 1 {
		t.Fatalf("expected 1 failure, got %d", failures)
	}
	if average <= 0 {
		t.Fatalf("expected average duration > 0, got %v", average)
	}

	metrics.Reset()
	attempts, failures, average = metrics.Snapshot()
	if attempts != 0 || failures != 0 || average != 0 {
		t.Fatalf("expected metrics to reset to zero, got attempts=%d failures=%d average=%v", attempts, failures, average)
	}
}
