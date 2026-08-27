# FIL8, the console file surface

FIL8 gives an operator a view of what an account stores, and a way to put a
file in and take one out. It also shows the two facts that explain a refusal:
how much room is left, and where the bytes land.

## One account, not the deployment

The five file routes scope every answer to the calling credential. No admin
route lists across accounts. A console page that claimed a deployment-wide view
would need a route that does not exist.

The page therefore shows the account the credential belongs to. An operator who
wants another account's files reads them with that account's credential. The
screen restates the rule the routes already hold. It does not work around it.

## The bound reaches the screen only when the console can read it

`limits.TightestStoredBytes` reads two holders: the account and the key. The
console can read the first through the admin surface and cannot read the second
at all.

| Credential | What the console can read | What it renders |
| --- | --- | --- |
| a console session | the default account's limit | the total against the bound |
| a pasted gateway key | neither limit reliably | the total alone |

A session resolves to the canonical account. That is the one case where the
gateway enforces the bound the console read. With a pasted key the total
renders with no ceiling beside it. A number that does not enforce the refusal is worse than no number. It tells
an operator there is room the account does not have.

The test named `without a session the total claims no bound` holds that.

## A partial list makes the total a floor

The list route caps a page at 1000 records and reports `has_more`. A sum over a
capped page is a floor, not the amount stored.

The view names which of the two it shows. With `has_more` set, it puts
`At least` in front of the total and says the count covers the newest files
only.

## The refusal is the gateway's own words

The `writeError` method on `FilesController` separates four causes. They are a
purpose the gateway does not serve, a retention window outside its range, an
invalid file, and an account with no room left. Each needs a different next
step. A delete repairs only the last one.

The upload notice therefore carries the message the gateway returned. A denied
credential is the one case the console rephrases. A 401 and a 403 name different
repairs, and `accessMessage` already owns that wording.

## The backend reaches the console through the admin surface

Stored bytes do not live in the record store, so `storage.type` does not
describe them. `GET /api/v1/admin/info` gained a `files.backend` field, read
from `blob.Store.Backend()`.

It sits on the admin surface rather than on a file route because it describes
the deployment and not any one file. A deployment that stores no files reports
`not configured` rather than omitting the field: an absent field reads as an
older gateway, which is a different fact.

`NewAdminController` gained a variadic option rather than a fourth positional
argument, so its four existing call sites stayed as they were.

## Deletion asks first

The gateway writes a stored file once and then deletes it. It never rewrites
one and never reuses an identifier. A delete is therefore final, and a request
that names the file afterward reads as a file that never existed. A row action that deleted on the first click would make one misplaced
click unrecoverable. The delete runs behind a confirmation that restates both
the filename and the identifier.

## Both file scopes reach a new key

The key form's non-admin set gained `files:read` and `files:write`. A key that
can send a document inline can store one and name it later, so withholding the
file scopes would not withhold the capability. It would only withhold the
cheaper way to use it.

## Acceptance

| Condition | Statement | Held by |
| --- | --- | --- |
| FIL-V20 | the console lists, uploads, and deletes a stored file | the seven tests below |

| Test | What it states |
| --- | --- |
| `the list renders every field the gateway records` | FIL-V20, first statement |
| `a file with no expiry says so rather than showing an empty cell` | a kept file and an unread expiry do not read alike |
| `a refused upload renders the reason the gateway gave` | FIL-V20, second statement |
| `the total renders against the account bound` | FIL-V20, third statement |
| `without a session the total claims no bound` | an unreadable ceiling is not claimed |
| `a capped list reports the total as a floor` | a partial sum says it is partial |
| `deleting asks first and sends the identifier only after the confirmation` | a final action is not one click away |

| Go test | What it states |
| --- | --- |
| `TestSystemInfoNamesTheFileBackend` | the admin surface reports where bytes land |
| `TestSystemInfoSaysWhenNoFileStorageExists` | a deployment with no file storage says so |

## Verification

Fail-before: the console had no file view, and the gate reported
`19 passed, 3 failed`.

After: `bash scripts/verify-files-api.sh` reports `20 passed, 2 failed`. The
two remaining conditions belong to FIL9.

The FIL-V20 check body changed with this task. It previously grepped one word
in one file. An empty route file satisfied that. It now asserts all three
operations in the panel and all three FIL-V20 anchors in the test file. The
terminal count stays at 22.

`npm run check` in `console` passes: build, `tsc --noEmit`, and 132 tests in 21
files. `bash scripts/verify-console-modernization.sh` reports `21 passed`.
`bash scripts/verify-auth-onboarding.sh` reports `26 passed`.
`bash scripts/verify-console-session-grants.sh` reports `16 passed`.
`go build ./...`, `go vet ./...`, and `go test ./...` pass. `make lint` reports
`0 issues`.
