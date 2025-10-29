package telemetry

import (
	"context"
	"sync/atomic"
	"time"
)

// CommitMetrics fasst Messwerte zu Commit-Versuchen zusammen.
type CommitMetrics struct {
	totalDuration atomic.Int64
	attempts      atomic.Uint64
	failures      atomic.Uint64
}

var defaultCommitMetrics CommitMetrics

// DefaultCommitMetrics liefert die globalen Metriken.
func DefaultCommitMetrics() *CommitMetrics {
	return &defaultCommitMetrics
}

// TraceCommit startet ein Commit-Span und liefert eine Abschlusstfunktion, die Dauer und Fehlerzustand meldet.
func TraceCommit(ctx context.Context) (context.Context, func(error)) {
	start := time.Now()
	defaultCommitMetrics.attempts.Add(1)
	return ctx, func(err error) {
		elapsed := time.Since(start)
		defaultCommitMetrics.totalDuration.Add(elapsed.Nanoseconds())
		if err != nil {
			defaultCommitMetrics.failures.Add(1)
		}
	}
}

// Snapshot gibt die gesammelten Werte zurück.
func (m *CommitMetrics) Snapshot() (attempts uint64, failures uint64, average time.Duration) {
	attempts = m.attempts.Load()
	failures = m.failures.Load()
	total := m.totalDuration.Load()
	if attempts == 0 {
		return attempts, failures, 0
	}
	average = time.Duration(total / int64(attempts))
	return attempts, failures, average
}

// Reset setzt alle Zähler zurück.
func (m *CommitMetrics) Reset() {
	m.totalDuration.Store(0)
	m.attempts.Store(0)
	m.failures.Store(0)
}
