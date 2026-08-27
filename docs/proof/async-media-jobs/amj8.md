# AMJ8 The console jobs surface

## Outcome

The console has a Jobs page. A reader picks a model that serves video, submits
a prompt, and watches the state move. A failed job names the reason on the row.
A finished job plays in the page. A finished job whose bytes went shows a
marker instead of a player.

The gate moves from 16 of 18 to 17 of 18. `AMJ-V17` passes. `AMJ-V18` is the
last one, and AMJ9 owns it.

## The window had to reach the caller

A reader with a page of finished jobs has to tell the ones it can still play
from the ones it can only read about. The job object carried no way to ask. The
record held `AssetExpiresAt`, and nothing on the wire reported it.

The only other way to ask is to fetch each asset and read the refusal. That
spends one request per row to learn that a page is unplayable. It also renders
a player first and an error second. A reader watching that concludes the
gateway is broken.

So `inference.VideoJob` grows `ExpiresUnix`, and both codecs publish it as
`expires_at`. The name is not a choice. The OpenAI video object already carries
the same fact under `expires_at`. A client written against that API therefore
reads this gateway without a change. The OpenRouter codec publishes the same
name, because a client that switched families would otherwise lose the answer.

The window travels only while there are bytes behind it. `canonicalVideoJob`
reads `job.HasAsset()` before it sets the field. The record keeps
`AssetExpiresAt` after the sweep takes the asset, so reporting it then would
point a caller at a video that is already gone. The test named
`TestAListedJobStatesWhetherItsBytesAreStillThere` drives the route through
both halves: the window reads on a stored asset, and the same job reports none
after `ExpireAsset`. The state stays `completed` across the two, because the
work happened.

## A player cannot fetch its own bytes

A `<video src="/v1/videos/{id}/content">` sends no `Authorization` header. A
reader holding a console session gets the bytes anyway. The session cookie is
`HttpOnly`, and the browser attaches it without help. A reader holding a pasted
gateway key would get 401. That is a working console for one credential and a
broken one for the other.

The helper `fetchJobAsset` therefore reads the bytes through the same
credential every other call uses. The component `JobPlayer` hands the
element an object URL
over the result, and revokes that URL when the component unmounts. A page left
open through a dozen videos would otherwise hold all of them.

Nothing fetches for a whole listing. A reader asks for one video at a time
through the Play control. A page of twenty finished jobs therefore costs one
listing request rather than twenty video downloads.

## The catalog decides what a reader can submit

The model chooser offers only models whose offerings name `videos-generations`.
Offering every model would let a reader submit a chat model and read a routing
refusal that says nothing about the mistake.

The operation string is the catalog's own spelling, which
`TestTheMediaOperationSpellingIsPinned` already pins. A provider that gains
video shows up in this chooser with no console change. A deployment that routes
none says so in a sentence rather than shows an empty list.

## The page stops polling when the work stops

`refetchInterval` reads the listing it already has. A page holding any job that
is not terminal re-reads every five seconds. A page holding only terminal jobs
returns `false` and stops. A terminal job never returns to running, so a poll
of one reads the same answer forever. A fixed interval would spend requests for
the rest of the day on a tab somebody left open.

The elapsed clock is separate state on a one-second tick. A running job's
elapsed time has to move while nothing else changes. A number that jumped in
five-second steps would read as a frozen page. A finished job counts against
`completed_at` rather than the clock, so the number stops when the work does.

## What the two required tests actually guard

`an expired job shows the marker and never a player` is the test the task
names. It asserts the marker and then asserts three separate absences: no
player testid, no `video` element anywhere in the document, and no Play
control. The first alone would pass a panel that rendered a bare `<video>` with
no testid.

`a failed job names the reason the provider gave` asserts the reason text
rather than the word `failed`. A row showing only the state sends a reader to
the gateway log to learn whether to change the prompt or the model.

A third test covers the case that keeps the second honest. A panel that never
offered a video at all would pass the expired test. The test named `a job whose
window is still open offers the video` covers that gap. It asserts that the
same `completed` state reads the other way while the window holds.

The mock throws from `fetchJobAsset`. No test in the file plays an asset, so a
call to it is a defect rather than a fixture gap.

## Evidence

```
pnpm --dir console check                         built, tsc clean, 136 tests passed
go build ./...                                   clean
go vet ./...                                     clean
go test ./...                                    all packages ok
make lint                                        0 issues
bash scripts/verify-async-media-jobs.sh          Summary: 17 passed, 1 failed
bash scripts/verify-console-modernization.sh     Summary: 21 passed, 0 failed
bash scripts/verify-openrouter-parity.sh         Summary: 16 passed, 0 failed
bash scripts/verify-model-modalities.sh          Summary: 26 passed, 0 failed
bash scripts/verify-files-api.sh                 Summary: 22 passed, 0 failed
bash scripts/verify-v1-architecture.sh           Summary: 12 passed, 0 failed
bash scripts/verify-dependency-direction.sh      Summary: 6 passed, 0 failed
bash scripts/verify-package-layout.sh            passed
```

The one failure is `AMJ-V18`. AMJ9 owns it.

New tests: 4 in `console/src/components/jobs/JobsPanel.test.tsx`, 1 in
`internal/server/video_content_test.go`, and 1 in each of
`internal/protocol/openai/video_jobs_test.go` and
`internal/protocol/openrouter/video_jobs_test.go`.
