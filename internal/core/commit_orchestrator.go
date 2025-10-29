package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/timzifer/committable_queue/internal/telemetry"
)

// Bank beschreibt eine Commit-fähige Partition.
//
// PrepareCommit liefert Publish-/Abort-Callbacks. Erst wenn alle Banken
// erfolgreich vorbereitet wurden, ruft der Orchestrator die Publish-Callbacks
// auf. Bei Fehlern oder Kontextabbruch werden die Abort-Callbacks in umgekehrter
// Reihenfolge ausgeführt.
type Bank interface {
	PrepareCommit(ctx context.Context) (publish func(), abort func(), err error)
}

// CommitOrchestrator serialisiert Commits über alle bekannten Banken.
type CommitOrchestrator struct {
	mu      sync.Mutex
	banks   []Bank
	version atomic.Uint64
}

type commitObserverKey struct{}

// WithCommitObserver returns a context that notifies observer about the final
// outcome of CommitAll. On success the observer is invoked immediately before
// the publish callbacks are executed; on failure it is invoked before the error
// is returned to the caller.
func WithCommitObserver(ctx context.Context, observer func(error)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, commitObserverKey{}, observer)
}

// NewCommitOrchestrator erzeugt einen neuen Orchestrator.
func NewCommitOrchestrator(banks ...Bank) *CommitOrchestrator {
	copyBanks := append([]Bank(nil), banks...)
	return &CommitOrchestrator{banks: copyBanks}
}

// CommitAll führt Commit auf allen Banken innerhalb einer globalen kritischen Sektion aus.
func (o *CommitOrchestrator) CommitAll(ctx context.Context) (err error) {
	ctx, finish := telemetry.TraceCommit(ctx)
	defer func() { finish(err) }()

	observer, _ := ctx.Value(commitObserverKey{}).(func(error))

	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.banks) == 0 {
		if observer != nil {
			observer(nil)
		}
		return nil
	}

	publishes := make([]func(), 0, len(o.banks))
	aborts := make([]func(), 0, len(o.banks))

	for _, bank := range o.banks {
		if err = ctx.Err(); err != nil {
			break
		}
		var publish, abort func()
		publish, abort, err = bank.PrepareCommit(ctx)
		if err != nil {
			break
		}
		if publish == nil {
			publish = func() {}
		}
		if abort == nil {
			abort = func() {}
		}
		publishes = append(publishes, publish)
		aborts = append(aborts, abort)
	}

	if err != nil {
		for i := len(aborts) - 1; i >= 0; i-- {
			aborts[i]()
		}
		if observer != nil {
			observer(err)
		}
		return err
	}

	if err = ctx.Err(); err != nil {
		for i := len(aborts) - 1; i >= 0; i-- {
			aborts[i]()
		}
		if observer != nil {
			observer(err)
		}
		return err
	}

	if observer != nil {
		observer(nil)
	}

	for _, publish := range publishes {
		publish()
	}

	o.version.Add(1)
	return nil
}

// Version gibt den aktuell veröffentlichten Commit-Stand zurück.
func (o *CommitOrchestrator) Version() uint64 {
	return o.version.Load()
}

// RegisterBank hängt zur Laufzeit eine weitere Bank an.
func (o *CommitOrchestrator) RegisterBank(bank Bank) error {
	if bank == nil {
		return errors.New("nil bank")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.banks = append(o.banks, bank)
	return nil
}
