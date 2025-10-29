// Package queue provides a segmented queue that separates visible and pending
// elements. Pending elements become visible only after a successful commit
// publish step.
//
// Commit is modelled as a two-phased operation via PrepareCommit. The prepare
// phase detaches the current pending segment and returns publish/abort
// callbacks. The caller can batch multiple prepares (for example via a
// multi-bank commit orchestrator) before atomically publishing all staged
// changes. Aborting a prepared commit restores the detached pending elements so
// that no data is lost when a later bank fails.
//
// Overflow handling happens only during the publish phase. When the merged
// visible segment exceeds the configured MaxLen, elements are dropped according
// to the configured DropPolicy before Publish releases its locks.
//
// The queue is safe for concurrent producers and consumers that interact with
// different segments. Operations on the visible and pending segments use their
// own internal locks, while the publish/abort steps serialise mutations via an
// additional mutex to maintain consistency.
package queue
