package queue

import (
	"sync"
)

type node[T any] struct {
	value T
	prev  *node[T]
	next  *node[T]
}

type deque[T any] struct {
	head *node[T]
	tail *node[T]
	len  int
	mu   sync.Mutex
}

func newDeque[T any]() *deque[T] {
	return &deque[T]{}
}

func (d *deque[T]) pushBack(value T) {
	d.mu.Lock()
	defer d.mu.Unlock()

	n := &node[T]{value: value}
	if d.len == 0 {
		d.head = n
		d.tail = n
	} else {
		n.prev = d.tail
		d.tail.next = n
		d.tail = n
	}
	d.len++
}

func (d *deque[T]) pushFront(value T) {
	d.mu.Lock()
	defer d.mu.Unlock()

	n := &node[T]{value: value}
	if d.len == 0 {
		d.head = n
		d.tail = n
	} else {
		n.next = d.head
		d.head.prev = n
		d.head = n
	}
	d.len++
}

func (d *deque[T]) popFront() (zero T, _ bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.len == 0 {
		return zero, false
	}

	current := d.head
	next := current.next
	if next != nil {
		next.prev = nil
	} else {
		d.tail = nil
	}
	d.head = next
	d.len--

	current.next = nil
	current.prev = nil

	return current.value, true
}

func (d *deque[T]) popBack() (zero T, _ bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.len == 0 {
		return zero, false
	}

	current := d.tail
	prev := current.prev
	if prev != nil {
		prev.next = nil
	} else {
		d.head = nil
	}
	d.tail = prev
	d.len--

	current.next = nil
	current.prev = nil

	return current.value, true
}

func (d *deque[T]) length() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.len
}

func (d *deque[T]) appendLocked(other *deque[T]) {
	if other.len == 0 {
		return
	}

	if d.len == 0 {
		d.head = other.head
		d.tail = other.tail
		d.len = other.len
	} else {
		other.head.prev = d.tail
		d.tail.next = other.head
		d.tail = other.tail
		d.len += other.len
	}

	other.head = nil
	other.tail = nil
	other.len = 0
}

type segmentedQueueOptions[T any] struct {
	initialVisible []T
	initialPending []T
}

type SegmentedQueueOption[T any] func(*segmentedQueueOptions[T])

func WithInitialVisible[T any](values ...T) SegmentedQueueOption[T] {
	return func(opts *segmentedQueueOptions[T]) {
		opts.initialVisible = append(opts.initialVisible[:0], values...)
	}
}

func WithInitialPending[T any](values ...T) SegmentedQueueOption[T] {
	return func(opts *segmentedQueueOptions[T]) {
		opts.initialPending = append(opts.initialPending[:0], values...)
	}
}

type SegmentedQueue[T any] struct {
	visible *deque[T]
	pending *deque[T]
	mu      sync.Mutex
	opts    segmentedQueueOptions[T]
}

func NewSegmentedQueue[T any](options ...SegmentedQueueOption[T]) *SegmentedQueue[T] {
	sq := &SegmentedQueue[T]{
		visible: newDeque[T](),
		pending: newDeque[T](),
	}

	for _, opt := range options {
		opt(&sq.opts)
	}

	for _, v := range sq.opts.initialVisible {
		sq.visible.pushBack(v)
	}
	for _, v := range sq.opts.initialPending {
		sq.pending.pushBack(v)
	}

	return sq
}

func (sq *SegmentedQueue[T]) PopFront() (T, bool) {
	return sq.visible.popFront()
}

func (sq *SegmentedQueue[T]) PopBack() (T, bool) {
	return sq.visible.popBack()
}

func (sq *SegmentedQueue[T]) LenVisible() int {
	return sq.visible.length()
}

func (sq *SegmentedQueue[T]) PushBackPending(value T) {
	sq.pending.pushBack(value)
}

func (sq *SegmentedQueue[T]) PushFrontPending(value T) {
	sq.pending.pushFront(value)
}

func (sq *SegmentedQueue[T]) Commit() {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	sq.visible.mu.Lock()
	sq.pending.mu.Lock()

	sq.visible.appendLocked(sq.pending)

	sq.pending.mu.Unlock()
	sq.visible.mu.Unlock()
}
