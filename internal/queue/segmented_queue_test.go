package queue

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSegmentedQueueBasicOperations(t *testing.T) {
	q := NewSegmentedQueue[int](WithInitialVisible(1, 2), WithInitialPending(3))

	if got := q.LenVisible(); got != 2 {
		t.Fatalf("expected initial visible length 2, got %d", got)
	}

	v, ok := q.PopFront()
	if !ok || v != 1 {
		t.Fatalf("expected PopFront to return 1,true got %v,%v", v, ok)
	}

	v, ok = q.PopBack()
	if !ok || v != 2 {
		t.Fatalf("expected PopBack to return 2,true got %v,%v", v, ok)
	}

	if _, ok := q.PopFront(); ok {
		t.Fatalf("expected PopFront to fail on empty visible segment")
	}

	q.PushFrontPending(5)
	q.PushBackPending(6)

	if q.LenVisible() != 0 {
		t.Fatalf("visible segment should remain empty before commit")
	}

	q.Commit()

	if got := q.LenVisible(); got != 3 {
		t.Fatalf("expected visible length 3 after commit, got %d", got)
	}

	expected := []int{5, 3, 6}
	for i, want := range expected {
		if v, ok := q.PopFront(); !ok || v != want {
			t.Fatalf("pop %d expected %d got %v,%v", i, want, v, ok)
		}
	}

	if _, ok := q.PopFront(); ok {
		t.Fatalf("expected visible segment to be empty")
	}
}

func TestSegmentedQueueConcurrentReadersAndWriters(t *testing.T) {
	const (
		totalValues   = 500
		readerWorkers = 4
		writerWorkers = 4
	)

	q := NewSegmentedQueue[int]()

	var produced atomic.Int64
	var consumed atomic.Int64

	seen := make([]bool, totalValues)
	var seenMu sync.Mutex

	var writerWG sync.WaitGroup
	writerWG.Add(writerWorkers)
	for i := 0; i < writerWorkers; i++ {
		go func() {
			defer writerWG.Done()
			for {
				next := int(produced.Add(1)) - 1
				if next >= totalValues {
					return
				}
				q.PushBackPending(next)
				runtime.Gosched()
			}
		}()
	}

	var readerWG sync.WaitGroup
	readerWG.Add(readerWorkers)
	for i := 0; i < readerWorkers; i++ {
		go func() {
			defer readerWG.Done()
			for {
				if consumed.Load() >= totalValues {
					return
				}
				if v, ok := q.PopFront(); ok {
					if v < 0 || v >= totalValues {
						t.Errorf("read value out of range: %d", v)
					} else {
						seenMu.Lock()
						if seen[v] {
							t.Errorf("duplicate value read: %d", v)
						}
						seen[v] = true
						seenMu.Unlock()
					}
					consumed.Add(1)
				} else {
					runtime.Gosched()
				}
			}
		}()
	}

	var commitWG sync.WaitGroup
	commitWG.Add(1)
	go func() {
		defer commitWG.Done()
		for consumed.Load() < totalValues {
			q.Commit()
			runtime.Gosched()
		}
		q.Commit()
	}()

	writerWG.Wait()
	for consumed.Load() < totalValues {
		q.Commit()
		runtime.Gosched()
	}
	readerWG.Wait()
	commitWG.Wait()

	q.Commit()
	if _, ok := q.PopFront(); ok {
		t.Fatalf("expected queue to be empty after all operations")
	}

	for i, ok := range seen {
		if !ok {
			t.Fatalf("value %d was not observed", i)
		}
	}
}

func TestSegmentedQueueCommitOverflowDropOldest(t *testing.T) {
	q := NewSegmentedQueue[int](
		WithInitialVisible(1, 2),
		WithOptions[int](Options{MaxLen: 3, DropPolicy: DropOldest}),
	)

	q.PushBackPending(3)
	q.PushBackPending(4)

	q.Commit()

	expected := []int{2, 3, 4}
	for i, want := range expected {
		got, ok := q.PopFront()
		if !ok || got != want {
			t.Fatalf("after commit drop-oldest pop %d expected %d got %v,%v", i, want, got, ok)
		}
	}

	if _, ok := q.PopFront(); ok {
		t.Fatalf("queue should contain only %d elements after overflow handling", len(expected))
	}
}

func TestSegmentedQueueCommitOverflowDropNewest(t *testing.T) {
	q := NewSegmentedQueue[int](
		WithInitialVisible(1, 2),
		WithOptions[int](Options{MaxLen: 3, DropPolicy: DropNewest}),
	)

	q.PushBackPending(3)
	q.PushBackPending(4)

	q.Commit()

	expected := []int{1, 2, 3}
	for i, want := range expected {
		got, ok := q.PopFront()
		if !ok || got != want {
			t.Fatalf("after commit drop-newest pop %d expected %d got %v,%v", i, want, got, ok)
		}
	}

	if _, ok := q.PopFront(); ok {
		t.Fatalf("queue should contain only %d elements after overflow handling", len(expected))
	}
}
