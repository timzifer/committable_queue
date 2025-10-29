package committablequeue

import (
	"errors"
	"sync"
)

// OverflowPolicy defines how the queue reacts when it reaches its maximum length.
type OverflowPolicy int

const (
	// OverflowError causes Push to return ErrQueueFull without adding the element.
	OverflowError OverflowPolicy = iota
	// OverflowDropOldest removes the oldest element to make space for the new one.
	OverflowDropOldest
	// OverflowDropNewest drops the element that is being pushed.
	OverflowDropNewest
)

// ErrQueueFull is returned when pushing into a full queue while the overflow policy is OverflowError.
var ErrQueueFull = errors.New("committablequeue: queue is full")

type node[T any] struct {
	value T
	prev  *node[T]
	next  *node[T]
}

// Queue implements a committable queue with a lightweight commit barrier between reader and writer.
type Queue[T any] struct {
	mu             sync.RWMutex
	head           *node[T]
	tail           *node[T]
	commit         *node[T]
	size           int
	uncommitted    int
	maxLength      int
	overflowPolicy OverflowPolicy
}

// NewQueue creates a new queue with the provided maximum length and overflow policy.
// A non-positive maxLength means the queue can grow without bound.
func NewQueue[T any](maxLength int, policy OverflowPolicy) *Queue[T] {
	return &Queue[T]{
		maxLength:      maxLength,
		overflowPolicy: policy,
	}
}

// Len returns the total number of elements currently stored in the queue.
func (q *Queue[T]) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.size
}

// CommittedLen returns the number of elements available to the reader.
func (q *Queue[T]) CommittedLen() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.size - q.uncommitted
}

// UncommittedLen returns the number of elements that still need to be committed.
func (q *Queue[T]) UncommittedLen() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.uncommitted
}

// Push appends a new element to the writer side of the queue.
// The returned boolean indicates whether an element had to be dropped because of the configured overflow policy.
func (q *Queue[T]) Push(value T) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	dropped := false
	if q.maxLength > 0 && q.size >= q.maxLength {
		switch q.overflowPolicy {
		case OverflowDropOldest:
			dropped = q.dropOldest()
		case OverflowDropNewest:
			return true, nil
		default:
			return false, ErrQueueFull
		}
	}

	q.appendNode(&node[T]{value: value})
	return dropped, nil
}

// Commit moves all writer-side elements to the reader-side portion of the queue.
// It returns the number of elements that became visible to the reader.
func (q *Queue[T]) Commit() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	moved := q.uncommitted
	if moved == 0 {
		return 0
	}

	q.commit = nil
	q.uncommitted = 0
	return moved
}

// PopFront removes and returns the oldest committed element.
func (q *Queue[T]) PopFront() (T, bool) {
	var zero T

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.size == 0 || q.size == q.uncommitted {
		return zero, false
	}

	node := q.head
	value := node.value
	q.unlinkNode(node, false)
	return value, true
}

// PopBack removes and returns the newest committed element.
func (q *Queue[T]) PopBack() (T, bool) {
	var zero T

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.size == 0 || q.size == q.uncommitted {
		return zero, false
	}

	var node *node[T]
	if q.uncommitted == 0 {
		node = q.tail
	} else {
		node = q.commit.prev
	}
	if node == nil {
		return zero, false
	}

	value := node.value
	q.unlinkNode(node, false)
	return value, true
}

// SnapshotCommitted returns a copy of the committed elements for inspection/testing.
func (q *Queue[T]) SnapshotCommitted() []T {
	q.mu.RLock()
	defer q.mu.RUnlock()

	committed := q.size - q.uncommitted
	if committed <= 0 {
		return nil
	}

	result := make([]T, 0, committed)
	for node := q.head; node != nil; node = node.next {
		if q.commit != nil && node == q.commit {
			break
		}
		result = append(result, node.value)
	}
	return result
}

func (q *Queue[T]) appendNode(n *node[T]) {
	if q.tail == nil {
		q.head = n
		q.tail = n
	} else {
		q.tail.next = n
		n.prev = q.tail
		q.tail = n
	}

	if q.uncommitted == 0 {
		q.commit = n
	}

	q.uncommitted++
	q.size++
}

func (q *Queue[T]) dropOldest() bool {
	if q.head == nil {
		return false
	}

	if q.commit != nil && q.commit == q.head {
		q.unlinkNode(q.head, true)
	} else {
		q.unlinkNode(q.head, false)
	}

	return true
}

func (q *Queue[T]) unlinkNode(n *node[T], uncommitted bool) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		q.head = n.next
	}

	if n.next != nil {
		n.next.prev = n.prev
	} else {
		q.tail = n.prev
	}

	if uncommitted {
		if q.commit == n {
			q.commit = n.next
		}
		if q.uncommitted > 0 {
			q.uncommitted--
		}
		if q.uncommitted == 0 {
			q.commit = nil
		}
	}

	q.size--
	if q.size == 0 {
		q.commit = nil
		q.uncommitted = 0
	}
}
