package queue

import (
	"context"
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

func TestSegmentedQueuePrepareCommitAbortRestoresPending(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushBackPending(1)
	q.PushBackPending(2)

	publish, abort, err := q.PrepareCommit(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if publish == nil || abort == nil {
		t.Fatalf("expected publish and abort callbacks")
	}

	q.PushBackPending(3)
	abort()

	if got := q.LenVisible(); got != 0 {
		t.Fatalf("visible segment should remain unchanged after abort, got len %d", got)
	}

	q.Commit()

	expected := []int{1, 2, 3}
	for i, want := range expected {
		got, ok := q.PopFront()
		if !ok || got != want {
			t.Fatalf("post-abort commit pop %d expected %d got %v,%v", i, want, got, ok)
		}
	}
}

func TestSegmentedQueuePrepareCommitPublishExcludesNewPending(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushBackPending(10)
	q.PushBackPending(11)

	publish, abort, err := q.PrepareCommit(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if abort == nil {
		t.Fatalf("expected abort callback")
	}

	q.PushBackPending(12)
	publish()

	values := []int{}
	for {
		if v, ok := q.PopFront(); ok {
			values = append(values, v)
		} else {
			break
		}
	}

	if len(values) != 2 || values[0] != 10 || values[1] != 11 {
		t.Fatalf("visible segment contained unexpected values after publish: %v", values)
	}

	q.Commit()
	if v, ok := q.PopFront(); !ok || v != 12 {
		t.Fatalf("pending element added after prepare should commit later, got %v,%v", v, ok)
	}
}

func TestSegmentedQueuePrepareCommitRespectsContext(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushBackPending(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	publish, abort, err := q.PrepareCommit(ctx)
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
	if publish != nil || abort != nil {
		t.Fatalf("callbacks must be nil on failure")
	}

	q.Commit()
	if v, ok := q.PopFront(); !ok || v != 1 {
		t.Fatalf("pending element should remain after cancelled prepare, got %v,%v", v, ok)
	}
}

func TestSegmentedQueuePushFrontPendingOnEmpty(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushFrontPending(1)
	if got := q.LenVisible(); got != 0 {
		t.Fatalf("visible segment should remain empty, got len %d", got)
	}

	publish, abort, err := q.PrepareCommit(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if publish == nil {
		t.Fatalf("expected publish callback")
	}
	if abort == nil {
		t.Fatalf("expected abort callback")
	}
}

func TestSegmentedQueuePopBackEmpty(t *testing.T) {
	q := NewSegmentedQueue[int]()
	if _, ok := q.PopBack(); ok {
		t.Fatalf("expected PopBack on empty queue to fail")
	}
}

func TestSegmentedQueueCommitWithContextErrorPanics(t *testing.T) {
	q := NewSegmentedQueue[int]()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic from commitWithContext on cancelled context")
		}
	}()

	q.commitWithContext(ctx)
}

func TestSegmentedQueuePublishIdempotent(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushBackPending(42)

	publish, abort, err := q.PrepareCommit(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if abort == nil {
		t.Fatalf("expected abort callback")
	}

	publish()
	if got := q.LenVisible(); got != 1 {
		t.Fatalf("expected single element after publish, got %d", got)
	}

	publish()
	if got := q.LenVisible(); got != 1 {
		t.Fatalf("publish should be idempotent, got visible len %d", got)
	}

	empty := &stagedCommit[int]{queue: q}
	empty.Publish()
}

func TestSegmentedQueueAbortIdempotent(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushBackPending(1)
	publish, abort, err := q.PrepareCommit(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if publish == nil || abort == nil {
		t.Fatalf("expected publish and abort callbacks")
	}

	abort()
	abort()

	empty := &stagedCommit[int]{queue: q}
	empty.Abort()
}

func TestSegmentedQueueAbortRestoresWhenPendingEmpty(t *testing.T) {
	q := NewSegmentedQueue[int]()
	q.PushBackPending(7)
	q.PushBackPending(8)

	publish, abort, err := q.PrepareCommit(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if publish == nil || abort == nil {
		t.Fatalf("expected callbacks from prepare")
	}

	abort()

	if got := q.LenVisible(); got != 0 {
		t.Fatalf("abort should not modify visible segment, got len %d", got)
	}

	if v, ok := q.PopFront(); ok {
		t.Fatalf("visible segment should remain empty, got value %v", v)
	}

	if got := q.pending.length(); got != 2 {
		t.Fatalf("pending length should be restored to 2, got %d", got)
	}
}

func TestDequeAppendLocked(t *testing.T) {
	dst := newDeque[int]()
	other := newDeque[int]()

	dst.mu.Lock()
	other.mu.Lock()
	dst.appendLocked(other)
	other.mu.Unlock()
	dst.mu.Unlock()

	if dst.length() != 0 {
		t.Fatalf("append from empty deque should keep destination empty")
	}

	other.pushBack(1)
	dst.mu.Lock()
	other.mu.Lock()
	dst.appendLocked(other)
	other.mu.Unlock()
	dst.mu.Unlock()

	if dst.length() != 1 {
		t.Fatalf("expected destination length 1, got %d", dst.length())
	}
	if other.length() != 0 {
		t.Fatalf("source should be cleared after append, got len %d", other.length())
	}

	other.pushBack(2)
	other.pushBack(3)
	dst.mu.Lock()
	other.mu.Lock()
	dst.appendLocked(other)
	other.mu.Unlock()
	dst.mu.Unlock()

	values := []int{}
	for {
		v, ok := dst.popFront()
		if !ok {
			break
		}
		values = append(values, v)
	}

	expected := []int{1, 2, 3}
	if len(values) != len(expected) {
		t.Fatalf("unexpected number of values: %v", values)
	}
	for i, want := range expected {
		if values[i] != want {
			t.Fatalf("expected %v, got %v", expected, values)
		}
	}
}
