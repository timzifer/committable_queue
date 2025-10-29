package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timzifer/committable_queue/internal/telemetry"
)

type bankFunc func(ctx context.Context) (func(), func(), error)

func (f bankFunc) PrepareCommit(ctx context.Context) (func(), func(), error) {
	return f(ctx)
}

func TestCommitAllIsSerialized(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	var running atomic.Int32
	var concurrent atomic.Bool
	var orderMu sync.Mutex
	var order []string

	names := []string{"A", "B", "C"}
	banks := make([]Bank, 0, len(names))
	for _, name := range names {
		bankName := name
		banks = append(banks, bankFunc(func(ctx context.Context) (func(), func(), error) {
			publish := func() {
				current := running.Add(1)
				if current > 1 {
					concurrent.Store(true)
				}
				time.Sleep(10 * time.Millisecond)
				orderMu.Lock()
				order = append(order, bankName)
				orderMu.Unlock()
				running.Add(-1)
			}
			return publish, nil, nil
		}))
	}

	orchestrator := NewCommitOrchestrator(banks...)

	var wg sync.WaitGroup
	attempts := 3
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := orchestrator.CommitAll(context.Background()); err != nil {
				t.Errorf("commit failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if concurrent.Load() {
		t.Fatalf("commits overlapped despite global lock")
	}

	if len(order) != attempts*len(names) {
		t.Fatalf("unexpected commit count: got %d want %d", len(order), attempts*len(names))
	}
	for i, name := range order {
		expected := names[i%len(names)]
		if expected != name {
			t.Fatalf("banks committed out of order at index %d: got %s want %s", i, name, expected)
		}
	}

	gotAttempts, gotFailures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if gotAttempts != uint64(attempts) {
		t.Fatalf("unexpected attempt count: got %d want %d", gotAttempts, attempts)
	}
	if gotFailures != 0 {
		t.Fatalf("unexpected failure count: %d", gotFailures)
	}
}

func TestCommitAllPublishesVersionAfterBanks(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	started := make(chan struct{})
	release := make(chan struct{})

	bank1 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		publish := func() {
			close(started)
			<-release
		}
		return publish, nil, nil
	})
	bank2 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		publish := func() {
			<-release
		}
		return publish, nil, nil
	})

	orchestrator := NewCommitOrchestrator(bank1, bank2)
	initialVersion := orchestrator.Version()
	if initialVersion != 0 {
		t.Fatalf("unexpected initial version: %d", initialVersion)
	}

	done := make(chan error, 1)
	go func() {
		done <- orchestrator.CommitAll(context.Background())
	}()

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("commit did not start in time")
	}

	if v := orchestrator.Version(); v != initialVersion {
		t.Fatalf("version was published too early: got %d want %d", v, initialVersion)
	}

	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("commit did not finish")
	}

	if v := orchestrator.Version(); v != initialVersion+1 {
		t.Fatalf("unexpected version after commit: got %d want %d", v, initialVersion+1)
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 {
		t.Fatalf("unexpected attempts count: got %d want %d", attempts, 1)
	}
	if failures != 0 {
		t.Fatalf("unexpected failure count: %d", failures)
	}
}

func TestCommitAllFailureDoesNotPublishVersion(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	errCommit := errors.New("bank failure")
	bank1 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return func() {}, nil, nil
	})
	bank2 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return nil, nil, errCommit
	})
	bank3Called := atomic.Bool{}
	bank3 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		bank3Called.Store(true)
		return func() {}, nil, nil
	})

	orchestrator := NewCommitOrchestrator(bank1, bank2, bank3)
	if err := orchestrator.CommitAll(context.Background()); !errors.Is(err, errCommit) {
		t.Fatalf("unexpected error: %v", err)
	}

	if bank3Called.Load() {
		t.Fatalf("banks after failing bank were still executed")
	}
	if orchestrator.Version() != 0 {
		t.Fatalf("version advanced despite failure")
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 {
		t.Fatalf("unexpected attempts count: got %d want %d", attempts, 1)
	}
	if failures != 1 {
		t.Fatalf("unexpected failure count: got %d want %d", failures, 1)
	}
}

func TestCommitAllAbortsOnFailure(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	abortedFirst := atomic.Bool{}

	bank1 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		publish := func() {}
		abort := func() { abortedFirst.Store(true) }
		return publish, abort, nil
	})
	bank2 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return nil, nil, errors.New("prepare failed")
	})

	orchestrator := NewCommitOrchestrator(bank1, bank2)
	if err := orchestrator.CommitAll(context.Background()); err == nil {
		t.Fatalf("expected error")
	}

	if abortedFirst.Load() != true {
		t.Fatalf("first bank abort callback not invoked")
	}
	if orchestrator.Version() != 0 {
		t.Fatalf("version advanced despite abort")
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 {
		t.Fatalf("unexpected attempts count: got %d want %d", attempts, 1)
	}
	if failures != 1 {
		t.Fatalf("unexpected failure count: got %d want %d", failures, 1)
	}
}
