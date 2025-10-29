# Committable Queue

![Test coverage](.github/badges/coverage.svg)

Committable Queue is a Go library that coordinates transactional commits across
independently managed banks. It provides a stop-the-world barrier for writers,
deterministic visibility guarantees for readers, and a two-phase protocol that
allows banks to prepare, publish, or roll back their local state in lockstep.

The project includes unit, integration, benchmark, and fuzz tests that stress
the orchestrator with varied timing, failure, and context-cancellation
scenarios. For a deeper architectural description, see
[`docs/architecture/commit.md`](docs/architecture/commit.md).

## Prerequisites

* Go `1.24` (matching [`go.mod`](go.mod))
* A POSIX-compatible shell for running the examples below

## Quick start

Clone the repository and run the core test suite:

```bash
git clone https://github.com/timzifer/committable_queue.git
cd committable_queue
go test ./...
```

To integrate the library into another project, import the orchestrator from the
`internal/core` package. The main types to look at are `CommitAll`, the
`Bank` interface, and the pending vs. published register structures that each
bank maintains. Sample usage can be found in [`queue`](queue), which provides
fixtures for the higher-level tests.

## Testing and verification

Run all regression tests:

```bash
go test ./...
```

Execute the micro-benchmarks to gauge performance of the commit orchestration
loop:

```bash
go test -bench=. -benchtime=100ms ./internal/core
```

Smoke-test the fuzz harness that targets the commit protocol:

```bash
go test -run=^$ -fuzz=FuzzCommitAll -fuzztime=5s ./internal/core
```

Continuous integration runs the same suite via
[`test-and-coverage.yml`](.github/workflows/test-and-coverage.yml) and updates
the coverage badge after successful runs on the `main` branch.

## Repository layout

```
.
├── internal/core        # Commit orchestration logic, interfaces, and telemetry
├── queue                # Higher-level queue abstractions and test fixtures
├── tests                # End-to-end scenarios that exercise real commit flows
└── docs/architecture    # Deep dives into the commit protocol and design
```

## Contributing

1. Fork the repository and create a topic branch.
2. Add or update tests alongside your changes.
3. Run `go test ./...` and the relevant benchmarks or fuzz tests.
4. Submit a pull request describing the scenario your change improves.

Issues and feature requests are welcome. Use GitHub issues to report bugs or to
propose enhancements to the commit protocol.
