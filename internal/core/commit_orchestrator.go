package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/timzifer/committable_queue/internal/telemetry"
)

// Bank beschreibt eine Commit-fähige Partition.
type Bank interface {
	Commit(ctx context.Context) error
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

	for _, bank := range o.banks {
		if err := ctx.Err(); err != nil {
			finish(err)
			return err
		}
		if err := bank.Commit(ctx); err != nil {
			finish(err)
			return err
		}
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
