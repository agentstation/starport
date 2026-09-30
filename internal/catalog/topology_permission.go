package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
)

const topologyPermissionMaxBytes = 64 << 10

// TopologyPermission retains original native permission evidence for a checked transfer.
// It grants no catalog selection, permission renewal, or admission authority.
type TopologyPermission struct {
	topology string
	producer *runtime.RetainedCatalogPermission
	record   []byte
}

type topologyPermissionRecord struct {
	Version  int    `json:"version"`
	Topology string `json:"topology"`
	Producer []byte `json:"producer"`
}

// Format excludes native identities and private runtime paths from diagnostics.
func (TopologyPermission) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog permission (private)")
}

// InspectPermission checks the accepted catalog's original receipt under current target settings.
// The caller separately checks materialization, selection, and the native writer fence.
// This operation starts no runtime, source transport, acquisition, or clock monitor.
func (c *CompiledTopology) InspectPermission(ctx context.Context, settings Settings, clock permission.ClockReading) (*TopologyPermission, error) {
	request, err := c.permissionRequest(settings)
	if err != nil {
		return nil, err
	}
	producer, err := runtime.InspectRetainedCatalogPermission(ctx, request, clock)
	if err != nil {
		return nil, err
	}
	record, err := json.Marshal(topologyPermissionRecord{1, c.digest, producer.Record()}, json.Deterministic(true))
	if err != nil || len(record) > topologyPermissionMaxBytes {
		return nil, recovery.ErrConflict
	}
	return &TopologyPermission{topology: c.digest, producer: producer, record: record}, nil
}

func (c *CompiledTopology) permissionRequest(settings Settings) (runtime.RetainedCatalogPermissionRequest, error) {
	if c == nil || !fleetChunkDigest(c.digest) {
		return runtime.RetainedCatalogPermissionRequest{}, recovery.ErrConflict
	}
	owner := settings.directoryOwner()
	if owner.Deployment != c.request.DestinationBoundary.DeploymentID {
		return runtime.RetainedCatalogPermissionRequest{}, recovery.ErrConflict
	}
	parsed, err := catalogconfig.Parse(settings.catalogValues())
	if err != nil {
		return runtime.RetainedCatalogPermissionRequest{}, err
	}
	policy, err := runtime.ResolveSourcePolicy(parsed.Options()...)
	if err != nil {
		return runtime.RetainedCatalogPermissionRequest{}, err
	}
	var head catalogs.CatalogAuthorityHead
	found := false
	for _, capsule := range c.inventory.Capsules {
		ref, err := capsule.reference()
		if err != nil {
			return runtime.RetainedCatalogPermissionRequest{}, err
		}
		if ref == c.inventory.Selection.Accepted {
			if found {
				return runtime.RetainedCatalogPermissionRequest{}, recovery.ErrConflict
			}
			head, found = capsule.Generation.Manifest.AuthorityHead, true
		}
	}
	if !found {
		return runtime.RetainedCatalogPermissionRequest{}, recovery.ErrConflict
	}
	return runtime.RetainedCatalogPermissionRequest{Directory: parsed.StateDirectory, Owner: owner, SchedulerIdentity: parsed.SchedulerIdentity, SourcePolicy: policy, AcceptedHead: head}, nil
}

// Record returns a private copy for the coordinator's immutable activation decision.
func (p *TopologyPermission) Record() ([]byte, error) {
	if p == nil || p.producer == nil || len(p.record) == 0 || len(p.record) > topologyPermissionMaxBytes {
		return nil, recovery.ErrConflict
	}
	return bytes.Clone(p.record), nil
}

// Digest identifies original evidence. It does not replace native owner checks.
func (p *TopologyPermission) Digest() string {
	if p == nil || p.producer == nil {
		return ""
	}
	return payloadDigest(p.record)
}

// CheckPermission rechecks original identities and expiry without extending receipt validity.
func (c *CompiledTopology) CheckPermission(ctx context.Context, settings Settings, clock permission.ClockReading, proof *TopologyPermission) error {
	if c == nil || proof == nil || proof.producer == nil || proof.topology != c.digest {
		return recovery.ErrConflict
	}
	request, err := c.permissionRequest(settings)
	if err != nil {
		return err
	}
	return proof.producer.Check(ctx, request, clock)
}

// InspectRetainedPermission reopens evidence sealed before any component release.
// The expected digest must come from the complete immutable activation decision.
func (c *CompiledTopology) InspectRetainedPermission(ctx context.Context, settings Settings, clock permission.ClockReading, record []byte, expectedSHA256 string) (*TopologyPermission, error) {
	if c == nil || ctx == nil || len(record) == 0 || len(record) > topologyPermissionMaxBytes || !fleetChunkDigest(expectedSHA256) {
		return nil, recovery.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if payloadDigest(record) != expectedSHA256 {
		return nil, recovery.ErrConflict
	}
	checked, err := c.InspectPermission(ctx, settings, clock)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(checked.record, record) {
		return nil, recovery.ErrConflict
	}
	return checked, nil
}
