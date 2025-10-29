package committablequeue

import "testing"

func TestQueueCommitAndPop(t *testing.T) {
	q := NewQueue[int](0, OverflowError)

	if moved := q.Commit(); moved != 0 {
		t.Fatalf("commit on empty queue returned %d", moved)
	}

	if dropped, err := q.Push(1); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	if dropped, err := q.Push(2); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}

	if got := q.CommittedLen(); got != 0 {
		t.Fatalf("expected no committed elements, got %d", got)
	}
	if got := q.UncommittedLen(); got != 2 {
		t.Fatalf("expected 2 uncommitted elements, got %d", got)
	}

	if moved := q.Commit(); moved != 2 {
		t.Fatalf("expected to commit 2 elements, got %d", moved)
	}

	if got := q.CommittedLen(); got != 2 {
		t.Fatalf("expected 2 committed elements, got %d", got)
	}

	if v, ok := q.PopFront(); !ok || v != 1 {
		t.Fatalf("expected PopFront to return 1,true got %v,%v", v, ok)
	}

	if v, ok := q.PopBack(); !ok || v != 2 {
		t.Fatalf("expected PopBack to return 2,true got %v,%v", v, ok)
	}

	if q.Len() != 0 || q.CommittedLen() != 0 || q.UncommittedLen() != 0 {
		t.Fatalf("expected queue to be empty after pops")
	}
}

func TestQueueCommitBoundary(t *testing.T) {
	q := NewQueue[int](0, OverflowError)

	if dropped, err := q.Push(1); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	q.Commit()

	if dropped, err := q.Push(2); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	if got := q.CommittedLen(); got != 1 {
		t.Fatalf("expected 1 committed element, got %d", got)
	}
	if got := q.UncommittedLen(); got != 1 {
		t.Fatalf("expected 1 uncommitted element, got %d", got)
	}
	if committed := q.SnapshotCommitted(); len(committed) != 1 || committed[0] != 1 {
		t.Fatalf("expected SnapshotCommitted to return [1], got %v", committed)
	}

	if v, ok := q.PopBack(); !ok || v != 1 {
		t.Fatalf("expected PopBack to remove last committed element 1, got %v,%v", v, ok)
	}
	if q.CommittedLen() != 0 {
		t.Fatalf("expected committed length to be 0 after removing sole element")
	}
	if q.UncommittedLen() != 1 {
		t.Fatalf("expected uncommitted length to remain 1")
	}
	if committed := q.SnapshotCommitted(); committed != nil {
		t.Fatalf("expected SnapshotCommitted to be nil when no committed elements, got %v", committed)
	}

	if moved := q.Commit(); moved != 1 {
		t.Fatalf("expected commit to move 1 element, got %d", moved)
	}
	if v, ok := q.PopFront(); !ok || v != 2 {
		t.Fatalf("expected PopFront to return 2,true got %v,%v", v, ok)
	}
}

func TestQueueOverflowDropOldestCommitted(t *testing.T) {
	q := NewQueue[int](3, OverflowDropOldest)

	for i := 0; i < 3; i++ {
		if dropped, err := q.Push(i); dropped || err != nil {
			t.Fatalf("push failed at %d dropped=%v err=%v", i, dropped, err)
		}
	}
	q.Commit()

	if dropped, err := q.Push(3); !dropped || err != nil {
		t.Fatalf("expected dropOldest to drop element, got dropped=%v err=%v", dropped, err)
	}
	if got := q.Len(); got != 3 {
		t.Fatalf("expected queue length to stay at capacity, got %d", got)
	}

	if moved := q.Commit(); moved != 1 {
		t.Fatalf("expected commit to move the newly appended element, got %d", moved)
	}

	expected := []int{1, 2, 3}
	if committed := q.SnapshotCommitted(); len(committed) != len(expected) {
		t.Fatalf("unexpected committed length: got %v want %v", committed, expected)
	} else {
		for i, v := range committed {
			if v != expected[i] {
				t.Fatalf("unexpected committed value at %d: got %d want %d", i, v, expected[i])
			}
		}
	}
}

func TestQueueOverflowDropOldestUncommitted(t *testing.T) {
	q := NewQueue[int](3, OverflowDropOldest)

	for i := 0; i < 3; i++ {
		if dropped, err := q.Push(i); dropped || err != nil {
			t.Fatalf("unexpected push result at %d dropped=%v err=%v", i, dropped, err)
		}
	}
	if dropped, err := q.Push(3); !dropped || err != nil {
		t.Fatalf("expected to drop oldest uncommitted element, got dropped=%v err=%v", dropped, err)
	}

	if got := q.UncommittedLen(); got != 3 {
		t.Fatalf("expected uncommitted length to stay at capacity, got %d", got)
	}

	if moved := q.Commit(); moved != 3 {
		t.Fatalf("expected commit to move 3 elements, got %d", moved)
	}

	expected := []int{1, 2, 3}
	if committed := q.SnapshotCommitted(); len(committed) != len(expected) {
		t.Fatalf("unexpected committed values: got %v want %v", committed, expected)
	} else {
		for i, v := range committed {
			if v != expected[i] {
				t.Fatalf("unexpected committed value at %d: got %d want %d", i, v, expected[i])
			}
		}
	}
}

func TestQueueOverflowDropNewest(t *testing.T) {
	q := NewQueue[int](2, OverflowDropNewest)

	if dropped, err := q.Push(1); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	q.Commit()
	if dropped, err := q.Push(2); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	q.Commit()

	if dropped, err := q.Push(3); !dropped || err != nil {
		t.Fatalf("expected push to drop newest element, got dropped=%v err=%v", dropped, err)
	}
	if moved := q.Commit(); moved != 0 {
		t.Fatalf("expected commit to move 0 elements after drop newest, got %d", moved)
	}
	expected := []int{1, 2}
	if committed := q.SnapshotCommitted(); len(committed) != len(expected) {
		t.Fatalf("unexpected committed state: got %v want %v", committed, expected)
	} else {
		for i, v := range committed {
			if v != expected[i] {
				t.Fatalf("unexpected committed value at %d: got %d want %d", i, v, expected[i])
			}
		}
	}
}

func TestQueueOverflowError(t *testing.T) {
	q := NewQueue[int](1, OverflowError)

	if dropped, err := q.Push(1); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}

	if _, err := q.Push(2); err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

func TestPopBackWithUncommittedTail(t *testing.T) {
	q := NewQueue[int](0, OverflowError)

	if dropped, err := q.Push(1); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	if dropped, err := q.Push(2); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}
	q.Commit()
	if dropped, err := q.Push(3); dropped || err != nil {
		t.Fatalf("unexpected push result dropped=%v err=%v", dropped, err)
	}

	if v, ok := q.PopBack(); !ok || v != 2 {
		t.Fatalf("expected PopBack to return last committed element 2, got %v,%v", v, ok)
	}
	if q.CommittedLen() != 1 {
		t.Fatalf("expected committed length to be 1 after PopBack, got %d", q.CommittedLen())
	}
	if q.UncommittedLen() != 1 {
		t.Fatalf("expected uncommitted length to remain 1, got %d", q.UncommittedLen())
	}

	if moved := q.Commit(); moved != 1 {
		t.Fatalf("expected commit to move the remaining uncommitted element, got %d", moved)
	}
	if v, ok := q.PopBack(); !ok || v != 3 {
		t.Fatalf("expected PopBack to return committed element 3, got %v,%v", v, ok)
	}
}
