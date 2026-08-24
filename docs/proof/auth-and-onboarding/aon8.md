# AON8 Local admin token

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V23 and AON-V24

## What changed

Every route into Starport up to here needed a gateway API key, and issuing a
gateway API key is itself an admin act. An operator who has just installed
Starport holds nothing, so there was no first move that did not already assume
one. AON8 breaks that circle with a credential the machine gives itself.

```text
internal/localauth        the token, the file, and the rules for touching it
starport auth token       print this machine's token
starport auth status      report its generation, age, and exposure answer
starport auth rotate      replace the secret and record that an operator asked
```

The gateway mints the token at startup. The CLI reads the same file. Neither
asks the other for permission.

## Two credentials, deliberately unalike

| | Gateway API key | Local admin token |
| --- | --- | --- |
| Belongs to | a tenant | nobody |
| Proves | who you are | where you are |
| Lives in | encrypted storage | one file, mode 0600 |
| Prefix | `STARPORT_` | `starport_local_` |
| Issued by | an admin act | the machine, on first start |
| Revoked by | deleting the key | rotating the file |

The prefixes are the part a person reads. They differ in case as well as in
words, so a token pasted into a field expecting the other is wrong at a glance,
in a terminal, in a log line, and in a support thread.
`TestTokenIsNotMistakableForAGatewayKey` holds that apart, and `Validate`
refuses a record whose secret
does not carry the local prefix — a gateway key hand-copied into the token file
is read as a corrupt record rather than honoured.

## The record

```json
{
  "version": 1,
  "secret": "starport_local_...",
  "generation": 2,
  "issued_at": "2026-08-24T02:23:48.351705Z",
  "scope": "local-admin",
  "rotated_at": "2026-08-24T02:23:48.351705Z"
}
```

`version` leads, because a hand-read file should open with the field that
decides whether the rest means anything. A record from a newer binary is refused
rather than guessed at: a field this version does not know about could be the
one carrying a restriction. `scope` is stored so a future narrower token cannot
be read as this one. `generation` counts mints, and is what an operator compares
after a rotation to see that it took.

The secret is 32 bytes from `crypto/rand`, base64url without padding. Comparison
is `subtle.ConstantTimeCompare`, and an empty candidate is refused before the
comparison rather than by it, so an absent token authorizes nobody.

## One token per machine, even when two gateways start at once

Every operation takes an exclusive lock on a sibling `.lock` file and every
write lands through a temporary file and a rename:

- The lock keeps two starts from minting different tokens on the same cold
  directory. If each minted its own, the second would overwrite the first, and
  the operator would be holding a value the running process does not accept.
- The rename keeps a reader from seeing half a record, including a reader not
  holding the lock at all.
- The temporary file is chmod 0600 **before** the content is written, so the
  secret is never on disk at a mode another account could read, however briefly.
- A rename carries the destination's mode, not the source's, so 0600 is asserted
  on the destination after the rename rather than assumed from the temporary
  file.
- Every failure path removes the temporary file. A stray file holding a valid
  secret is worse than the error that produced it.

`TestConcurrentStartsMintOneToken` runs eight goroutines, each opening its own
`Store` — sharing one would test nothing, because the contention being proven is
between file descriptors. Exactly one reports that it minted; all eight hold the
same secret.

The lock wait is bounded at five seconds. Two starts contend for milliseconds;
anything near that bound is a stale lock or a wedged process, and failing to
start with a clear message beats hanging with none.

## An unreadable file is refused, not replaced

A token file that exists but does not decode is an error on every read path.
Minting over it would throw away a credential the operator may still be holding,
and they would find out when the token they have stops working.

`rotate` is the deliberate exception. Refusing to read is the safe answer while
the old secret might still matter; refusing to rotate as well would leave a
machine with a broken file and no command that fixes it. So `rotate` repairs:

| State of the file | `token` / `status` / startup | `rotate` |
| --- | --- | --- |
| missing | mints (startup, `token`) or reports none (`status`) | mints generation 1 |
| valid | reads it | replaces it, generation + 1 |
| not JSON | `ErrCorruptRecord` | repairs, generation 1 |
| newer layout | `ErrUnsupportedVersion` | repairs, generation 1 |
| another scope | `ErrCorruptRecord` | repairs, generation 1 |
| a gateway key | `ErrCorruptRecord` | repairs, generation 1 |

`TestUnreadableRecordsAreRefusedRatherThanReplaced` asserts the file survives
byte-identical through the refusal.

## The exposure tripwire

```go
func AllowsExposure(bindHost string, token Token) bool {
	return authmode.LoopbackHost(bindHost) || token.Rotated()
}
```

A never-rotated token is the one this machine printed when it first started.
That value has been in a terminal, and a terminal is scrollback, a tmux buffer,
a screen share, and a CI log. It is safe where it was born and nowhere else. On
loopback the only callers are already on this machine and holding the token
proves nothing they could not do anyway; on an address the network can reach it
becomes a credential, and a first-boot secret is not one.

AON6 wrote the rule for what counts as this machine. AON8 calls it rather than
restating it — the direct lesson from AON7, where two spellings of one rule had
to be guarded by a drift test until the rule moved to one owner.

The way out is a rotation, not an acknowledgment flag. AON6's tripwire has
`--allow-remote-no-auth` because an operator can genuinely mean to run an open
gateway. There is no equivalent here, because acknowledging this risk leaves the
operator holding the same compromised value. Rotating leaves them holding a
secret that was never printed at boot.

The refusal lives in composition rather than configuration validation, because
it depends on what is on disk: the same configuration is safe with a rotated
token and unsafe with a first-boot one, and validation does not read files.

```text
the local admin token has never been rotated and this gateway binds 0.0.0.0:
the first-boot token was printed to this machine's terminal, so it is safe only
where it was born. Run "starport auth rotate" and start again
```

`localauth.RotateCommand` is one constant. The startup refusal and the
`auth status` line both name it, because a refusal that spelled the command
differently from the one that fixes it would send an operator looking for a
command that does not exist.

## One path, no second knob

`Paths.LocalTokenFile` sits under the data directory rather than beside
`config.env`: it is state this machine generated, not a decision an operator
wrote down. `SecurityConfig.LocalTokenPath` carries **no** environment tag on
purpose. The gateway reads the path from configuration and the CLI reads it from
`Paths`, and both derive from `PathsForConfigDir`. A second knob would let the
two disagree, and a `starport auth rotate` that writes a file the running
gateway never reads is worse than no command at all. An operator who needs the
file elsewhere moves everything with `STARPORT_CONFIG_DIR`.

Composition fails closed on an empty path rather than picking one, because the
loader always fills it in: an empty value means the configuration did not come
from the loader, and guessing would put a credential somewhere the CLI never
looks.

## What the commands print

`auth token` prints the secret and nothing else, so it composes — `starport auth
token | pbcopy` has to copy a token and not a paragraph about one. It mints if
the machine is cold, so an operator who runs it before ever starting the gateway
gets a credential instead of an instruction to go start one.

`auth status` never prints the secret. It is the command an operator runs in
front of other people, and a credential in that output is a credential in a
screen share. It reports the exposure answer, because knowing whether a public
bind will start is the reason to run it.

`auth rotate` says what changed and what did not:

```text
Rotated the local admin token to generation 2.
Token file: …/data/local-admin-token.json

starport_local__Y2nNw38Jdw5kig0fieUyY2BGiaTuVveNt_D8PJIZw0

A running gateway keeps the token it read at startup. Restart it for this one
to take effect.
```

Without that last line, an operator rotates, tries the new token against the
process that is still running, and concludes the command is broken.

## Fail-before

On the AON7 head (`f4dbb3e`):

```text
$ starport auth status
unknown command "auth"
exit=2

$ ls internal/localauth
No such file or directory
```

## Tests

| Test | What it holds |
| --- | --- |
| `TestMintProducesADistinctCredentialEachTime` | 64 mints, no repeat; rotation is not theatre |
| `TestTokenIsNotMistakableForAGatewayKey` | the prefixes stay apart where a person reads them |
| `TestMintRefusesGenerationZero` | a generation is always meaningful |
| `TestAuthorizesMatchesExactly` | no prefix, case-folded, or empty-accepts-empty match |
| `TestRedactedDropsTheSecret` | reporting paths cannot leak it, and redaction does not mutate |
| `TestAllowsExposure` | seven host and rotation combinations |
| `TestRotatedRefusesAZeroTimestamp` | a hand-edited empty time does not lift the refusal |
| `TestValidateNamesWhatIsWrong` | version, secret shape, and issue time |
| `TestConcurrentStartsMintOneToken` | eight parallel starts, one mint, one shared secret |
| `TestTokenFileIsOwnerOnly` | 0600 after the first write and after a rotation over 0644 |
| `TestRotateReplacesTheSecretAndSaysSo` | new secret, generation + 1, rotation time, reload matches |
| `TestRotateWorksOnAColdMachine` | the instruction in the refusal works before any start |
| `TestLoadReportsAColdMachineAsNotFound` | "nothing yet" is a state, not a disk failure |
| `TestUnreadableRecordsAreRefusedRatherThanReplaced` | four bad records survive byte-identical |
| `TestRotateRepairsAnUnreadableRecord` | the machine is never left unfixable |
| `TestWritesLeaveNoStraySecrets` | no temporary file holding a real token survives |
| `TestNewStoreRequiresAnAbsolutePath` | a relative path is a different file per invocation |
| `TestStartupMintsTheTokenTheCLIReads` | the gateway's token is the one the CLI prints |
| `TestASecondStartKeepsTheFirstToken` | a restart does not invalidate a copied token |
| `TestANetworkBindRefusesAFirstBootToken` | the acceptance case, and the message names the command |
| `TestANetworkBindAcceptsARotatedToken` | the refusal is one an operator can get past |
| `TestLoopbackAcceptsAFirstBootToken` | a laptop starts with no ceremony |
| `TestCompositionRefusesAConfigurationWithNowhereToKeepTheToken` | fails closed on an empty path |
| `TestStartupRefusesAnUnreadableTokenRatherThanReplacingIt` | startup is not the destructive path |
| `TestAuthTokenPrintsOnlyTheSecret` | one line, and a gateway would accept it |
| `TestAuthTokenIsStableAcrossCalls` | running it twice does not hand out two credentials |
| `TestAuthStatusOnAColdMachineIsNotAFailure` | the first command a confused operator runs |
| `TestAuthStatusNeverPrintsTheSecret` | text and JSON, against the real minted value |
| `TestAuthStatusReportsTheExposureAnswer` | before and after a rotation |
| `TestAuthRotateSaysWhatDidNotChange` | the running gateway keeps its token |
| `TestAuthRotateWorksOnAColdMachine` | end to end through the command |
| `TestAuthNamesItsSubcommands` | `starport auth` is discoverable |
| `TestAuthRejectsAnUnknownSubcommand` | a typo is an error, not a silent no-op |

## Repository gates

```bash
go test ./internal/localauth/ ./internal/cli/ ./internal/app/   # ok
go test ./internal/localauth/ -race -count=3                    # ok
bash scripts/verify-auth-onboarding.sh                          # 24 passed, 2 failed
```

The two remaining failures are AON-V25 (the launch route) and AON-V26 (the
provider screens), which AON9 and AON10 own.

## What AON8 deliberately did not do

**No expiry.** Nimbus has a rotation-freshness rule with a maximum age. The plan
asks only that a never-rotated auto-minted token be stale, and this
implementation stops there. A local credential that expires on a schedule locks
an operator out of their own console on a machine they are sitting at, which is
worse than the risk it addresses.

**No HTTP acceptance.** Nothing serves or reads the token over HTTP yet. AON9
owns the launch ticket and the console session that the token will authorize;
minting the credential first is what makes that work possible, and wiring a
half-designed acceptance path here would be a second thing to unpick.

**No console surface.** An operator reaches the token through the CLI only. The
console asks for it in AON9, when there is a sign-in flow to ask inside of.
