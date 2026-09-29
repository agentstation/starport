# Fleet adoption fixture

`publication.json` contains a small catalog and its real retained inputs. Starmap
produced it with `fleetValidationFixture` in the published module
`v0.16.6-0.20260929053147-9ab700ad930f`. Both `ValidateFleetRecovery` and
`ValidateFleetReplay` accepted the exported publication.

The tests replace the fixture lease with a real native lease before publication.
They retain native Valkey and PostgreSQL operations, fencing, and history validation.
They also retain backend replacement, concurrency, and lost-response recovery.
`TestFleetAdoptionAcceptsRestoredClosedBoundary` retains the full embedded product
catalog, runtime setup, acquisition policy, and public preparation API.

To regenerate, copy that published Starmap module into a writable temporary
directory. Add this temporary test in its `runtime` package and run
`go test ./runtime -run '^TestExportAdoptionFixture$' -count=1`:

```go
func TestExportAdoptionFixture(t *testing.T) {
    snapshot, _ := fleetValidationFixture(t)
    if err := ValidateFleetRecovery(t.Context(), snapshot); err != nil {
        t.Fatal(err)
    }
    if err := ValidateFleetReplay(t.Context(), snapshot); err != nil {
        t.Fatal(err)
    }
    data, err := json.Marshal(snapshot.Publication)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile("publication.json", data, 0600); err != nil {
        t.Fatal(err)
    }
}
```

The temporary file imports `encoding/json/v2`, `os`, and `testing`. Format the
output as JSON and copy it here. Do not edit the encoded generation or retained
input bytes independently. The consumer tests validate their binding and replay
with the currently pinned Starmap module.
