package integration

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/timzifer/committable_queue/internal/core"
)

type registerState struct {
	value     uint16
	version   uint64
	timestamp time.Time
}

type registerPair struct {
	left  registerState
	right registerState
}

type registerBank struct {
	name       string
	mu         sync.RWMutex
	visible    registerState
	pending    registerState
	hasPending bool
}

func newRegisterBank(name string, initial registerState) *registerBank {
	return &registerBank{name: name, visible: initial}
}

func (b *registerBank) PrepareCommit(ctx context.Context) (func(), func(), error) {
	b.mu.Lock()
	if !b.hasPending {
		b.mu.Unlock()
		return func() {}, nil, nil
	}

	staged := b.pending
	b.hasPending = false
	b.mu.Unlock()

	publish := func() {
		b.mu.Lock()
		b.visible = staged
		b.mu.Unlock()
	}

	abort := func() {
		b.mu.Lock()
		if !b.hasPending {
			b.pending = staged
			b.hasPending = true
		}
		b.mu.Unlock()
	}

	return publish, abort, nil
}

func (b *registerBank) updatePending(state registerState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = state
	b.hasPending = true
}

func (b *registerBank) snapshot() registerState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.visible
}

func performModbusRead(left, right *registerBank) registerPair {
	return registerPair{left: left.snapshot(), right: right.snapshot()}
}

func TestMultiBankCommitProducesConsistentSnapshot(t *testing.T) {
	initialTimestamp := time.Unix(1700000000, 0).UTC()
	initialStateLeft := registerState{value: 100, version: 0, timestamp: initialTimestamp}
	initialStateRight := registerState{value: 200, version: 0, timestamp: initialTimestamp}

	leftBank := newRegisterBank("holding", initialStateLeft)
	rightBank := newRegisterBank("input", initialStateRight)

	orchestrator := core.NewCommitOrchestrator(leftBank, rightBank)

	// Reader should observe the initial, consistent snapshot.
	initialPair := performModbusRead(leftBank, rightBank)
	if initialPair.left.version != initialPair.right.version {
		t.Fatalf("initial snapshot has diverging versions: %+v", initialPair)
	}
	if !initialPair.left.timestamp.Equal(initialTimestamp) || !initialPair.right.timestamp.Equal(initialTimestamp) {
		t.Fatalf("initial snapshot has unexpected timestamp: %+v", initialPair)
	}

	// Concurrent writers prepare the next values with matching version/timestamp per bank.
	nextVersion := initialPair.left.version + 1
	nextTimestamp := initialTimestamp.Add(10 * time.Millisecond)
	writerStart := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(2)

	go func() {
		defer writers.Done()
		<-writerStart
		leftBank.updatePending(registerState{value: 111, version: nextVersion, timestamp: nextTimestamp})
	}()

	go func() {
		defer writers.Done()
		<-writerStart
		rightBank.updatePending(registerState{value: 222, version: nextVersion, timestamp: nextTimestamp})
	}()

	close(writerStart)
	writers.Wait()

	// Even after the pending updates, the reader still sees the previously committed pair.
	beforeCommitPair := performModbusRead(leftBank, rightBank)
	if beforeCommitPair.left.version != initialPair.left.version || beforeCommitPair.right.version != initialPair.right.version {
		t.Fatalf("reader observed version change before commit: before=%+v initial=%+v", beforeCommitPair, initialPair)
	}
	if !beforeCommitPair.left.timestamp.Equal(initialTimestamp) || !beforeCommitPair.right.timestamp.Equal(initialTimestamp) {
		t.Fatalf("reader observed timestamp change before commit: before=%+v initial=%+v", beforeCommitPair, initialPair)
	}

	// Reader waits for the next consistent pair (same version & timestamp across banks).
	commitDone := make(chan struct{})
	preCommitPairs := make(chan registerPair, 1)
	committedPairs := make(chan registerPair, 1)
	go func() {
		for {
			candidate := performModbusRead(leftBank, rightBank)
			if candidate.left.version == nextVersion &&
				candidate.right.version == nextVersion &&
				candidate.left.timestamp.Equal(nextTimestamp) &&
				candidate.right.timestamp.Equal(nextTimestamp) {
				select {
				case <-commitDone:
					committedPairs <- candidate
				default:
					preCommitPairs <- candidate
				}
				return
			}
			runtime.Gosched()
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		err := orchestrator.CommitAll(context.Background())
		errCh <- err
		close(commitDone)
	}()

	select {
	case pair := <-preCommitPairs:
		t.Fatalf("next consistent pair became visible before commit finished: %+v", pair)
	case <-commitDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("commit did not finish in time")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	select {
	case pair := <-committedPairs:
		if pair.left.version != pair.right.version {
			t.Fatalf("committed pair versions differ: %+v", pair)
		}
		if !pair.left.timestamp.Equal(pair.right.timestamp) {
			t.Fatalf("committed pair timestamps differ: %+v", pair)
		}
		if pair.left.version != nextVersion {
			t.Fatalf("unexpected committed version: got %d want %d", pair.left.version, nextVersion)
		}
		if !pair.left.timestamp.Equal(nextTimestamp) {
			t.Fatalf("unexpected committed timestamp: got %s want %s", pair.left.timestamp, nextTimestamp)
		}
	case <-time.After(time.Second):
		t.Fatalf("reader never observed the committed pair")
	}
}
