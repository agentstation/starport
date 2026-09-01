# Starport process boundary

This package owns signals, process streams, exit codes, and application
composition. The command tree and its tests live in `internal/cli`.

`main.go` passes process resources to `run.go`. The latter builds injected CLI
dependencies, resolves platform paths, and maps returned errors to one process
exit code. It passes server and initialization work through explicit runtime
boundaries.

The package also owns the commands that bind to the build: the catalog verbs
in `models.go` read the embedded catalog generation, and `agent.go` installs
the embedded skill from `skills/starport`. Both register through the
`ExtraCommands` dependency, so `internal/cli` stays free of build-bound
behavior.
