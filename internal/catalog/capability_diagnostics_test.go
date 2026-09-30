package catalog

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/agentstation/starport/internal/recovery"
)

func TestCatalogCapabilityDiagnosticsRedactCopiedValues(t *testing.T) {
	const marker = "SYNTHETIC-PRIVATE-CATALOG-MARKER"
	cases := []struct {
		name       string
		pointer    any
		nilPointer any
		want       string
	}{
		{"archive", &FleetHistoricalArchive{data: &fleetHistoricalData{Boundary: recovery.Record{DeploymentID: marker}}}, (*FleetHistoricalArchive)(nil), "historical fleet catalog (private)"},
		{"inventory", &TopologyInventory{digest: marker, data: topologyInventoryData{Boundary: recovery.Record{DeploymentID: marker}}}, (*TopologyInventory)(nil), "catalog topology inventory (private)"},
		{"materialization", &TopologyMaterialization{topology: marker, digest: marker}, (*TopologyMaterialization)(nil), "catalog materialization (private)"},
		{"compiled", &CompiledTopology{digest: marker, recoveryRecord: []byte(marker), expected: map[string][]byte{marker: []byte(marker)}}, (*CompiledTopology)(nil), "compiled catalog topology (private)"},
		{"permission", &TopologyPermission{topology: marker, record: []byte(marker)}, (*TopologyPermission)(nil), "catalog permission (private)"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := reflect.ValueOf(test.pointer).Elem().Interface()
			for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
				for _, candidate := range []any{test.pointer, value} {
					if fmt.Sprintf(verb, candidate) != test.want {
						t.Fatal("capability diagnostics disclosed private fields", verb)
					}
				}
				if got := fmt.Sprintf(verb, test.nilPointer); got != "<nil>" && got != test.want {
					t.Fatal("nil capability diagnostics failed", verb)
				}
			}
		})
	}
}
