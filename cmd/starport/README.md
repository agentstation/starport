# Starport process boundary

This package owns signals, process streams, exit codes, and application
composition. The command tree and its tests live in `internal/cli`.

`main.go` passes process resources to `run.go`. The latter builds injected CLI
dependencies and maps returned errors to one process exit code. It does not own
command behavior.
