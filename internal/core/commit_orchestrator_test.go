package core

import (
	"context"
	"errors"
	"fmt"
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

func TestCommitAllAbortsInReverseOrder(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	var abortOrder []int
	var mu sync.Mutex

	makeBank := func(id int) Bank {
		return bankFunc(func(ctx context.Context) (func(), func(), error) {
			publish := func() {}
			abort := func() {
				mu.Lock()
				abortOrder = append(abortOrder, id)
				mu.Unlock()
			}
			return publish, abort, nil
		})
	}

	banks := []Bank{makeBank(0), makeBank(1), makeBank(2)}
	failingBank := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return nil, nil, errors.New("boom")
	})

	orchestrator := NewCommitOrchestrator(append(banks, failingBank)...)
	if err := orchestrator.CommitAll(context.Background()); err == nil {
		t.Fatalf("expected error")
	}

	expected := []int{2, 1, 0}
	if len(abortOrder) != len(expected) {
		t.Fatalf("unexpected abort count: got %d want %d", len(abortOrder), len(expected))
	}
	for i, want := range expected {
		if abortOrder[i] != want {
			t.Fatalf("unexpected abort order at index %d: got %d want %d", i, abortOrder[i], want)
		}
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

func TestCommitAllCancelsContextDuringPreparation(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	ctx, cancel := context.WithCancel(context.Background())

	aborted := atomic.Bool{}

	bank1 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		publish := func() {}
		abort := func() { aborted.Store(true) }
		return publish, abort, nil
	})

	bank2 := bankFunc(func(ctx context.Context) (func(), func(), error) {
		cancel()
		return nil, nil, ctx.Err()
	})

	orchestrator := NewCommitOrchestrator(bank1, bank2)
	err := orchestrator.CommitAll(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}

	if !aborted.Load() {
		t.Fatalf("expected first bank abort to run")
	}
	if orchestrator.Version() != 0 {
		t.Fatalf("version advanced despite cancellation")
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 {
		t.Fatalf("unexpected attempts count: got %d want %d", attempts, 1)
	}
	if failures != 1 {
		t.Fatalf("unexpected failure count: got %d want %d", failures, 1)
	}
}

func TestRegisterBank(t *testing.T) {
	telemetry.DefaultCommitMetrics().Reset()

	var publishCount atomic.Int32

	firstBank := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return func() { publishCount.Add(1) }, nil, nil
	})

	orchestrator := NewCommitOrchestrator(firstBank)

	if err := orchestrator.RegisterBank(nil); err == nil {
		t.Fatalf("expected error when registering nil bank")
	}

	secondBank := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return func() { publishCount.Add(1) }, nil, nil
	})
	if err := orchestrator.RegisterBank(secondBank); err != nil {
		t.Fatalf("registering bank failed: %v", err)
	}

	if err := orchestrator.CommitAll(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if publishCount.Load() != 2 {
		t.Fatalf("unexpected publish count: got %d want %d", publishCount.Load(), 2)
	}
	if orchestrator.Version() != 1 {
		t.Fatalf("unexpected version after commit: %d", orchestrator.Version())
	}

	attempts, failures, _ := telemetry.DefaultCommitMetrics().Snapshot()
	if attempts != 1 {
		t.Fatalf("unexpected attempts count: got %d want %d", attempts, 1)
	}
	if failures != 0 {
		t.Fatalf("unexpected failure count: got %d want %d", failures, 0)
	}
}

func TestCommitObserverRunsBeforePublish(t *testing.T) {
	var orderMu sync.Mutex
	order := make([]string, 0, 2)

	bank := bankFunc(func(ctx context.Context) (func(), func(), error) {
		publish := func() {
			orderMu.Lock()
			order = append(order, "publish")
			orderMu.Unlock()
		}
		return publish, nil, nil
	})

	orchestrator := NewCommitOrchestrator(bank)

	ctx := WithCommitObserver(context.Background(), func(err error) {
		if err != nil {
			t.Fatalf("unexpected observer error: %v", err)
		}
		orderMu.Lock()
		order = append(order, "observer")
		orderMu.Unlock()
	})

	if err := orchestrator.CommitAll(ctx); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	orderMu.Lock()
	defer orderMu.Unlock()
	if len(order) != 2 {
		t.Fatalf("unexpected callback count: %d", len(order))
	}
	if order[0] != "observer" || order[1] != "publish" {
		t.Fatalf("unexpected callback order: %v", order)
	}
}

func TestCommitObserverReceivesError(t *testing.T) {
	errPrepare := errors.New("prepare failed")
	bank := bankFunc(func(ctx context.Context) (func(), func(), error) {
		return nil, nil, errPrepare
	})

	orchestrator := NewCommitOrchestrator(bank)

	var observed error
	ctx := WithCommitObserver(context.Background(), func(err error) {
		observed = err
	})

	err := orchestrator.CommitAll(ctx)
	if !errors.Is(err, errPrepare) {
		t.Fatalf("unexpected commit error: %v", err)
	}
	if !errors.Is(observed, errPrepare) {
		t.Fatalf("observer saw wrong error: %v", observed)
	}
}

func BenchmarkCommitAll(b *testing.B) {
	ctx := context.Background()
	bankCounts := []int{1, 4, 16, 64}

	for _, count := range bankCounts {
		b.Run(fmt.Sprintf("%dBanks", count), func(b *testing.B) {
			banks := make([]Bank, count)
			for i := range banks {
				banks[i] = bankFunc(func(ctx context.Context) (func(), func(), error) {
					return func() {}, nil, nil
				})
			}

			orchestrator := NewCommitOrchestrator(banks...)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := orchestrator.CommitAll(ctx); err != nil {
					b.Fatalf("commit failed: %v", err)
				}
			}
		})
	}
}

func FuzzCommitAll(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{1})
	f.Add([]byte{2})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			t.Skip()
		}

		if len(data) > 8 {
			data = data[:8]
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		orchestrator := NewCommitOrchestrator()

		for i, b := range data {
			mode := b % 4
			idx := i

			switch mode {
			case 0:
				orchestrator.RegisterBank(bankFunc(func(ctx context.Context) (func(), func(), error) {
					return func() {}, nil, nil
				}))
			case 1:
				orchestrator.RegisterBank(bankFunc(func(ctx context.Context) (func(), func(), error) {
					return func() {}, func() {}, nil
				}))
			case 2:
				orchestrator.RegisterBank(bankFunc(func(ctx context.Context) (func(), func(), error) {
					return nil, nil, errors.New("prepare failed")
				}))
			case 3:
				orchestrator.RegisterBank(bankFunc(func(ctx context.Context) (func(), func(), error) {
					if idx%2 == 0 {
						cancel()
						return nil, nil, ctx.Err()
					}
					return func() {}, nil, nil
				}))
			}

		}

		err := orchestrator.CommitAll(ctx)
		if err != nil {
			if orchestrator.Version() != 0 {
				t.Fatalf("version advanced despite error: %d", orchestrator.Version())
			}
		} else {
			if orchestrator.Version() != 1 {
				t.Fatalf("unexpected version on success: %d", orchestrator.Version())
			}
		}

		// Ensure the orchestrator can be safely reused after fuzz scenario.
		_ = orchestrator.RegisterBank(bankFunc(func(ctx context.Context) (func(), func(), error) {
			return func() {}, nil, nil
		}))
	})
}
