# Populated operator command qualification

The test uses the original private activation request and the selected primary configuration file.
It loads `config.env` through `config.NewLoader` before preparation and on every command.
It does not replace the loaded configuration with decoded fixture fields.

The source owns the account, gateway API keys, SQL grant, reservations, and uncertain provider job.
The file service creates the linked file and records its bytes through the storage meter before backup.
Independent history retains the actual post-backup revocation, grant withdrawal, spending, and uncertain dispatch.
The target uses isolated Valkey, PostgreSQL, and object storage selections.
The actual budget owner binds shared admission to SQL authority time.
The recovery scan retains its native cursor until the complete bounded census finishes.

`TestRecoveryPopulatedOperatorCommandsAcrossNativePhaseCut` checks these contracts:

- An incomplete interval fails before configuration loading.
- A missing original payload fails before native imports reopen.
- Status leaves the original activation and history files unchanged.
- A new process resumes after the blob owner's committed reply is lost.
- Exact command retries retain the original decision and history bytes.
- The restored file record still resolves to its original bytes.
- Revoked authorization stays absent, and spending and held reservations remain intact.
- The uncertain provider attempt does not repeat.
- A fresh application must pass its own admission readiness check.
- A later withdrawal stays effective after status and exact activation retries.

The process cut occurs after the native blob commit and before the application writes its phase record.
The test closes the old native handles before the new process runs the actual CLI command.
This proves reply-loss recovery across a process boundary.
It does not prove an operating-system kill during an in-flight command.

The `shipping-binary` subtest executes the source-built `cmd/starport` binary for status and an exact completed retry.
Set `STARPORT_RECOVERY_OPERATOR_BINARY` to its absolute path.
Without that binary, the subtest reports `UNVERIFIED` and skips.
Recorded qualification requires no skips.

Build and test with the selected Go 1.27.1 toolchain and `GOWORK=off`.
The race run needs a race-built executable.
The pure-Go run needs an executable built with `CGO_ENABLED=0`.
Supply native fixture credentials through the private fixture wrapper.
Do not print configuration, connection strings, or private process output.
Keep all other writers fenced until fresh gateway readiness succeeds.

The whole Go package keeps its existing 30-minute test limit.
Each recovery command keeps its three-minute operation deadline.

This test qualifies one populated local-to-fleet command procedure.
It uses a bounded catalog fixture, not the full embedded catalog or the maximum inventory.
The simulated provider owns the lost-response evidence and makes no paid provider call.
External fencing and complete history remain operator responsibilities.
The command deadline is not an RPO or RTO target.
HTTP listener readiness, the other topology directions, and complete A16/A33 qualification remain separate evidence.
