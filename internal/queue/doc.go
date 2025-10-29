// Package queue provides a segmented queue that separates visible and pending
// elements. Pending elements become visible only during Commit operations.
//
// Overflow handling happens only during Commit. When the merged visible segment
// exceeds the configured MaxLen, elements are dropped immediately according to
// the configured DropPolicy before Commit releases its locks.
//
// The queue is safe for concurrent producers and consumers that interact with
// different segments. Operations on the visible and pending segments use their
// own internal locks, while Commit serialises segment merges via an additional
// mutex to maintain consistency.
package queue
