# CSG6 — The auth CLI verbs

## Problem

CSG5 shipped a page that tells a reader to run `starport auth token` and paste
what it prints. The command printed the token to a terminal and stopped there,
so the paste went through the scrollback of a long-lived shell. The page was
asking the operator to do work the CLI could do, and to leave a credential
behind while doing it.

## What was already there

Most of the plan's step was already shipped. `internal/cli/ui.go` registers
`--open` and `--copy` on `starport auth url`, and `internal/cli/launcher.go`
owns `BrowserOpener`, `ClipboardWriter`, `TerminalCheck`, the per-platform
clipboard table, and `ErrNoClipboard`. The plan named a new
`internal/cli/clipboard.go`; nothing needed it, and adding one would have split
an owner that already exists.

## Change

`starport auth token` gained `--copy`. `internal/cli/auth.go` grew one function,
`copyAuthToken`, and one usage rule.

## Why `--copy` does not also print

`auth url --copy` prints its link anyway, and this command does not. The two
credentials have different lifetimes: a launch ticket is dead in ninety seconds,
so a copy of it in the scrollback is a copy of nothing by the time anyone reads
the screen. The local admin token outlives the terminal it was printed in.

So the successful path leaves the secret in a clipboard the operator overwrites
within the minute and out of a scrollback they may screen-share tomorrow. That
is the flag's entire purpose; a `--copy` that also printed would be a no-op with
a side effect.

The default is unchanged. Without the flag the command still prints the secret
and nothing else, so `starport auth token | pbcopy` and
`-H "Authorization: Bearer $(starport auth token)"` compose as before.

## Why a copy failure still prints, and still fails

A container, or a host reached over SSH, has no clipboard command this build
knows how to drive. Two things have to be true there at once. The operator ends
up holding the token — a flag that swallowed the credential it was asked to hand
over would be worse than one that never existed — and the command exits
non-zero, so a script cannot read the fallback as a copy. `ErrNoClipboard` names
the alternative (`starport auth url | pbcopy`), because piping is what the
operator would have done anyway.

## Why `--copy` and `--json` are refused

`--json` exists to be read by a program; `--copy` targets the desktop of the
person typing. Honouring both would put a JSON document containing the secret on
the clipboard: not what the script wanted, and not what the operator meant to
paste. It exits 2, the usage code, rather than picking one silently.

## Verifier condition CSG-V13

The condition was written against a string literal (`grep_q '"copy"'`). The flag
name lives in the `flagCopy` constant `auth url` already used, so the literal
never appears — and a grep for `"copy"` would also have been satisfied by an
unrelated word in a comment. It now greps for `copyAuthToken`, which is the
function that does the work, and its description says what it checks. The
campaign's condition count is unchanged, so the contract table still holds.

## Tests

`internal/cli/auth_test.go`, five cases. They drive the real command through
`Run` with `Desktop.CopyToClipboard` replaced, which is the seam
`internal/cli/launcher.go` already exposes for exactly this.

| Test | Holds |
| --- | --- |
| `TestAuthTokenCopiesInsteadOfPrinting` | The secret reaches the clipboard, does not reach the output, and is the value a gateway reading the same file would accept. |
| `TestAuthTokenWithoutCopyReachesNoClipboard` | Nothing leaves the process unasked; the default path is untouched. |
| `TestAuthTokenCopyFallsBackToPrintingAndStillFails` | Both halves of the no-clipboard case: the token is printed, and the exit code is still 1. |
| `TestAuthTokenRefusesCopyWithJSON` | Exit code 2, nothing copied, and no secret in the output of a refused run. |
| `TestAuthHelpNamesTheDesktopVerbs` | `--copy` on `token`, `--copy` and `--open` on `url`, anchored to the option list. |

The help assertion is a regexp anchored on the option line rather than a
substring. Control 1 below is why: a substring match on `--copy` is also
satisfied by `--copy-control`, so the first version of that test reported a
renamed flag as a present one. The control caught it, not the review.

## Fail-before

The plan named the help-output control. All four ran against the change and were
restored immediately.

1. **The token subcommand does not register `--copy`** (`Name: flagCopy` renamed):

   ```
   --- FAIL: TestAuthTokenCopiesInsteadOfPrinting
       Error: Received unexpected error:
   --- FAIL: TestAuthHelpNamesTheDesktopVerbs/token
       Error: Expect "NAME: …" to match "(?m)^\s+--copy[\s,]"
   ```

2. **`--copy` also prints the secret:**

   ```
   Error: "starport_local_Uktnu…\nCopied the local admin token to the clipboard.\n"
          should not contain "starport_local_Uktnu…"
   ```

3. **A copy failure returns without printing:**

   ```
   Error: "" does not contain "starport_local_Rof2g…"
   ```

4. **`--copy` with `--json` is accepted:**

   ```
   Error: An error is expected but got nil.
   ```

## Manual run

Against a throwaway `STARPORT_CONFIG_DIR`, with the real `pbcopy` path and the
prior clipboard contents saved and restored:

```
$ starport auth token --copy
Copied the local admin token to the clipboard.
exit=0
  clipboard: starport_local_A…   token file: starport_local_A…

$ starport auth token --copy --json
starport auth token does not accept --copy with --json
exit=2
```

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | `Summary: 13 passed, 3 failed` — matches the CSG6 target |
| `go test ./internal/cli/...` | ok |
| `go test ./...` | all packages ok |
| `go vet ./...` | clean |
| `make lint` | clean |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-developer-experience.sh` | 47 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-doc-links.sh` | passed |
