# Committable Queue

![Test coverage](.github/badges/coverage.svg)

Committable Queue orchestrates transactional commits across independently managed
banks to ensure consistent batch processing. The project includes regression,
benchmark, and fuzz tests that exercise the commit orchestrator under diverse
conditions.

## Development

```bash
go test ./...
```

Run the benchmark and fuzz smoke tests locally with:

```bash
go test -bench=. -benchtime=100ms ./internal/core
go test -run=^$ -fuzz=FuzzCommitAll -fuzztime=5s ./internal/core
```

The CI workflow in [`.github/workflows/test-and-coverage.yml`](.github/workflows/test-and-coverage.yml)
executes these checks on every push and pull request, updating the coverage
badge after successful runs on the main branch.
