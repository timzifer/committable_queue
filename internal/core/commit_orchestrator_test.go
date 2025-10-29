package core

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/timzifer/committable_queue/internal/telemetry"
)

type testBank struct {
	prepare func(context.Context) (func(), func(), error)
}

func (tb *testBank) PrepareCommit(ctx context.Context) (func(), func(), error) {
	return tb.prepare(ctx)
}

func TestWithCommitObserver(t *testing.T) {
	base := context.Background()
	if WithCommitObserver(base, nil) != base {
		t.Fatalf("nil observer should return original context")
	}

	ctx := WithCommitObserver(base, func(error) {})
	if ctx == nil {
		t.Fatalf("context must not be nil")
	}
	if _, ok := ctx.Value(commitObserverKey{}).(func(error)); !ok {
		t.Fatalf("observer function not stored in context")
	}
}

func TestCommitOrchestratorNoBanks(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()
	orchestrator := NewCommitOrchestrator()

	var observed []error
	ctx := WithCommitObserver(context.Background(), func(err error) {
		observed = append(observed, err)
	})

	if err := orchestrator.CommitAll(ctx); err != nil {
		t.Fatalf("expected no error with zero banks, got %v", err)
	}

	if orchestrator.Version() != 0 {
		t.Fatalf("version should remain zero when nothing was committed")
	}

	if len(observed) != 1 || observed[0] != nil {
		t.Fatalf("observer should be invoked with nil error, got %v", observed)
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 || failures != 0 {
		t.Fatalf("metrics mismatch: attempts=%d failures=%d", attempts, failures)
	}
}

func TestCommitOrchestratorSuccess(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	var mu sync.Mutex
	publishes := []string{}

	bank1 := &testBank{prepare: func(context.Context) (func(), func(), error) {
		return func() {
				mu.Lock()
				publishes = append(publishes, "bank1")
				mu.Unlock()
			}, func() {
				t.Fatalf("abort should not be called on success")
			}, nil
	}}
	bank2 := &testBank{prepare: func(context.Context) (func(), func(), error) {
		return nil, nil, nil
	}}

	orchestrator := NewCommitOrchestrator(bank1, bank2)

	var observed []error
	ctx := WithCommitObserver(context.Background(), func(err error) {
		mu.Lock()
		observed = append(observed, err)
		mu.Unlock()
	})

	if err := orchestrator.CommitAll(ctx); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if orchestrator.Version() != 1 {
		t.Fatalf("version should increment on successful commit, got %d", orchestrator.Version())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 || observed[0] != nil {
		t.Fatalf("observer should record nil error, got %v", observed)
	}

	if len(publishes) != 1 || publishes[0] != "bank1" {
		t.Fatalf("unexpected publish sequence: %v", publishes)
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 || failures != 0 {
		t.Fatalf("metrics mismatch: attempts=%d failures=%d", attempts, failures)
	}
}

func TestCommitOrchestratorDefaultsForNilCallbacks(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	bank := &testBank{prepare: func(context.Context) (func(), func(), error) {
		return nil, nil, nil
	}}

	orchestrator := NewCommitOrchestrator(bank)
	if err := orchestrator.CommitAll(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if orchestrator.Version() != 1 {
		t.Fatalf("expected version increment, got %d", orchestrator.Version())
	}
}

func TestCommitOrchestratorPrepareErrorTriggersAbort(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	var mu sync.Mutex
	aborts := []string{}

	prepareErr := errors.New("prepare failed")

	bank1 := &testBank{prepare: func(context.Context) (func(), func(), error) {
		return func() {}, func() {
			mu.Lock()
			aborts = append(aborts, "bank1")
			mu.Unlock()
		}, nil
	}}
	bank2 := &testBank{prepare: func(context.Context) (func(), func(), error) {
		return nil, nil, prepareErr
	}}

	orchestrator := NewCommitOrchestrator(bank1, bank2)

	var observed error
	ctx := WithCommitObserver(context.Background(), func(err error) {
		observed = err
	})

	err := orchestrator.CommitAll(ctx)
	if err == nil {
		t.Fatalf("expected error from CommitAll")
	}
	if !errors.Is(err, prepareErr) {
		t.Fatalf("unexpected error: %v", err)
	}
	if observed == nil {
		t.Fatalf("observer should receive error notification")
	}

	mu.Lock()
	if len(aborts) != 1 || aborts[0] != "bank1" {
		t.Fatalf("expected abort for first bank, got %v", aborts)
	}
	mu.Unlock()

	if orchestrator.Version() != 0 {
		t.Fatalf("version should remain zero on failure, got %d", orchestrator.Version())
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 || failures != 1 {
		t.Fatalf("metrics mismatch: attempts=%d failures=%d", attempts, failures)
	}
}

func TestCommitOrchestratorContextCancellationBeforePrepare(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	bank := &testBank{prepare: func(context.Context) (func(), func(), error) {
		called = true
		return nil, nil, nil
	}}

	orchestrator := NewCommitOrchestrator(bank)
	if err := orchestrator.CommitAll(ctx); err == nil {
		t.Fatalf("expected context error")
	}
	if called {
		t.Fatalf("bank should not be called when context already cancelled")
	}
}

func TestCommitOrchestratorContextCancellationAfterPrepare(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var observed error
	ctx := WithCommitObserver(baseCtx, func(err error) {
		observed = err
	})

	aborted := false
	bank := &testBank{prepare: func(context.Context) (func(), func(), error) {
		cancel()
		return func() {
				t.Fatalf("publish must not be called when context is cancelled")
			}, func() {
				aborted = true
			}, nil
	}}

	orchestrator := NewCommitOrchestrator(bank)
	if err := orchestrator.CommitAll(ctx); err == nil {
		t.Fatalf("expected context cancellation error")
	}
	if !aborted {
		t.Fatalf("abort should run when context cancelled after prepare")
	}
	if observed == nil {
		t.Fatalf("observer should receive cancellation error")
	}
}

func TestCommitOrchestratorRegisterBank(t *testing.T) {
	orchestrator := NewCommitOrchestrator()
	if err := orchestrator.RegisterBank(nil); err == nil {
		t.Fatalf("expected error for nil bank")
	}

	bank := &testBank{prepare: func(context.Context) (func(), func(), error) {
		return nil, nil, nil
	}}
	if err := orchestrator.RegisterBank(bank); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if len(orchestrator.banks) != 1 {
		t.Fatalf("expected one registered bank, got %d", len(orchestrator.banks))
	}
}
