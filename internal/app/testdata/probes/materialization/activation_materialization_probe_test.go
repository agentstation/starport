package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
)

// This fixture-only probe uses the producer codec and actual stopped-directory materialization.
func TestStarportActivationMaterializationProbe(t *testing.T) {
	body, err := os.ReadFile(os.Getenv("STARPORT_MATERIALIZATION_PROBE_FIXTURE"))
	if err != nil || len(body) > 1<<20 {
		t.Fatal("invalid private materialization fixture")
	}
	var fixture struct {
		Generation catalogs.Generation
		Values     map[string]string
		Target     struct {
			Directory         string
			Owner             DirectoryOwner
			SchedulerIdentity string
		}
		Sources []sources.SourceActivity
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	opts := []Option{WithCatalogSource("file"), WithSourceURL(fixture.Values["STARMAP_CATALOG_SOURCE_URL"]), WithSourceStartupPolicy("require_source"), WithSourcePollInterval(0), WithAcquisitionEnabled(false), WithStartupSpread(0), WithStateDirectory(fixture.Target.Directory), WithSchedulerIdentity(fixture.Target.SchedulerIdentity), WithDirectoryOwner(fixture.Target.Owner), WithAcquirer(activationFixtureProviders{}), WithSourceAcquirer(activationFixtureMetadata{fixture.Sources})}
	config := defaults()
	if _, err := config.apply(opts...); err != nil {
		t.Fatal(err)
	}
	config.resolve()
	description, err := describeSources(config)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := catalogs.DecodeCatalogGeneration(fixture.Generation)
	if err != nil {
		t.Fatal(err)
	}
	layers := layerSet{publisherID: "bounded-activation-fixture", embeddedManifest: &fixture.Generation.Manifest, embedded: starmap.CatalogState{Catalog: catalog, GenerationID: fixture.Generation.Manifest.GenerationID, PayloadChecksum: fixture.Generation.Manifest.Payload.Checksum, GeneratedAt: fixture.Generation.Manifest.GeneratedAt}, sourceConfiguration: description}
	data, err := encodeFleetRecoveryWithPin(t.Context(), layers, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := catalogManifestChecksum(fixture.Generation)
	if err != nil {
		t.Fatal(err)
	}
	input := CatalogMaterializationInput{Generation: fixture.Generation, Recovery: CatalogRecovery{ManifestChecksum: manifest, Inputs: FleetRecovery{GenerationID: fixture.Generation.Manifest.GenerationID, PayloadChecksum: fixture.Generation.Manifest.Payload.Checksum, Checksum: fleetRecoveryChecksum(data), Data: data}}}
	if err := PrepareCatalogRecoveryDirectory(t.Context(), fixture.Target.Directory, fixture.Target.Owner, fixture.Target.SchedulerIdentity); err != nil {
		t.Fatal(err)
	}
	_, err = MaterializeCatalogRecovery(t.Context(), CatalogMaterializationRequest{Directory: fixture.Target.Directory, Owner: fixture.Target.Owner, SchedulerIdentity: fixture.Target.SchedulerIdentity, OperationID: "bounded-source-baseline", Inputs: []CatalogMaterializationInput{input}}, opts...)
	if err != nil {
		t.Fatal(err)
	}
}

// These inert fixture capabilities preserve the actual exported source configuration and refuse acquisition.
type activationFixtureProviders struct{}

func (activationFixtureProviders) AcquireProviders(context.Context, AcquisitionRequest) (AcquisitionResult, error) {
	return AcquisitionResult{}, errors.New("fixture acquisition is prohibited")
}

type activationFixtureMetadata struct{ sources []sources.SourceActivity }

func (m activationFixtureMetadata) SourceConfiguration() []sources.SourceActivity { return m.sources }
func (activationFixtureMetadata) AcquireSources(context.Context, SourceAcquisitionRequest) ([]sources.Observation, error) {
	return nil, errors.New("fixture acquisition is prohibited")
}
