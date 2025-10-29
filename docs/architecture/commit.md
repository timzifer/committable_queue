# Commit Orchestration

This document explains how the commit protocol coordinates multiple queue banks
so that each commit attempt is atomic, ordered, and observable.

## Stop-the-world phase

* **Global writer lock:** `CommitAll` acquires a process-wide mutex. While the
  lock is held, no other writer (`CommitAll` invocation) can start, and readers
  continue to observe the last successfully published snapshot.
* **Reader freeze:** No new version is published while the lock is held. Readers
  therefore operate on the previously exposed state and remain isolated from the
  commit attempt in progress.
* **Context awareness:** The call accepts a `context.Context`. Cancellation is
  checked before and after blocking operations so that a stalled commit can be
  abandoned promptly, releasing the global lock for the next writer.

## Two-phase protocol

1. **Prepare:** Each bank implements `PrepareCommit`. The orchestrator invokes
   these methods serially and collects pairs of publish/abort callbacks. During
   this stage, pending registers are only staged—readers still see the last
   committed values.
2. **Abort on failure:** If a bank returns an error from `PrepareCommit`, or if
   the context has been cancelled, the orchestrator executes all collected abort
   callbacks in reverse order. This rolls staged data back into the pending
   buffers.
3. **Publish:** Only when all banks report success (and the context remains
   active) does the orchestrator invoke the publish callbacks in the order they
   were registered. The callbacks copy staged data into their published
   registers, making the new snapshot visible.
4. **Version bump:** After every bank publishes successfully, the orchestrator
   atomically increments the global version counter so that readers can detect
   the availability of a new snapshot.

The prepare/publish split ensures that banks never expose partial state, even if
they maintain distinct storage backends or transport layers.

## Failure handling and observability

* **Short-circuiting:** When a `PrepareCommit` call fails, the orchestrator stops
  iterating over the remaining banks and immediately initiates rollback.
* **Guaranteed rollback:** Because the publish callbacks have not yet executed,
  no bank has surfaced staged data. Abort callbacks restore the pending state so
  that the next commit attempt starts from a consistent baseline.
* **Metrics:** Each commit attempt reports duration, success, and failure counts
  via `internal/telemetry/commit_metrics.go`. These counters feed the exported
  metrics registry and can be scraped by monitoring tools.
* **Error propagation:** A bank error is returned unchanged to the caller of
  `CommitAll`. If the context is cancelled, the orchestrator propagates the
  context error instead, allowing upstream services to correlate the failure.

### Timeline of a commit attempt

```
Acquire writer lock → Check context → Prepare bank A → … → Prepare bank N
         ↘ error? abort prepared banks ↴                   ↘ success
          Release lock, return error              Publish banks in order
                                                  Increment global version
                                                  Release lock, return nil
```

This timeline highlights the serialization guarantees: only one writer proceeds
at a time, and any failure skips the publish branch entirely.

## Deterministic multi-register reads

* **Version consistency:** Multi-register readers (for example, Modbus clients)
  must process values whose `Version` and `Timestamp` fields match. Visible
  registers change only after `CommitAll` finishes successfully.
* **Writer staging:** Writers update pending registers for each bank. During
  `PrepareCommit`, those values are staged temporarily; if a failure occurs, the
  abort callbacks rehydrate the pending buffers.
* **Visibility:** While `CommitAll` is executing, readers stay on the last
  published snapshot. Only after every bank publishes and the global version is
  incremented do the new register values become atomically visible.

## Related components

* `internal/core/commit.go` contains the orchestrator and its lock management.
* `queue/fixtures` provides in-memory test doubles that exercise the protocol.
* `tests/commit_all_test.go` (and related suites) validate the stop-the-world
  semantics, error propagation, and version sequencing described above.
