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

// NewCommitOrchestrator erzeugt einen neuen Orchestrator.
func NewCommitOrchestrator(banks ...Bank) *CommitOrchestrator {
	copyBanks := append([]Bank(nil), banks...)
	return &CommitOrchestrator{banks: copyBanks}
}

// CommitAll führt Commit auf allen Banken innerhalb einer globalen kritischen Sektion aus.
func (o *CommitOrchestrator) CommitAll(ctx context.Context) error {
	ctx, finish := telemetry.TraceCommit(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.banks) == 0 {
		finish(nil)
		return nil
	}

	publishes := make([]func(), 0, len(o.banks))
	aborts := make([]func(), 0, len(o.banks))

	var prepareErr error
	for _, bank := range o.banks {
		if err := ctx.Err(); err != nil {
			prepareErr = err
			break
		}
		publish, abort, err := bank.PrepareCommit(ctx)
		if err != nil {
			prepareErr = err
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

	if prepareErr != nil {
		for i := len(aborts) - 1; i >= 0; i-- {
			aborts[i]()
		}
		finish(prepareErr)
		return prepareErr
	}

	if err := ctx.Err(); err != nil {
		for i := len(aborts) - 1; i >= 0; i-- {
			aborts[i]()
		}
		finish(err)
		return err
	}

	for _, publish := range publishes {
		publish()
	}

	o.version.Add(1)
	finish(nil)
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
