package recovery

import "github.com/agentstation/starmap/pkg/productpaths"

// BackupFile identifies a selected local file and its portable artifact name.
// Relative names preserve the original layout within the owning file role.
type BackupFile struct {
	ArtifactID     string `json:"artifact_id"`
	Role           string `json:"role"`
	Relative       string `json:"relative"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	Source         string `json:"-"`
}

// BackupRole records how capture accounts for each canonical file role.
// Adapter snapshots own engine files. Source caches can be rebuilt under source policy.
type BackupRole struct {
	Entry        productpaths.FileEntry         `json:"entry"`
	Capture      string                         `json:"capture"`
	Observations []productpaths.FileObservation `json:"observations,omitempty"`
}

// BackupInventory records selected paths without fetching external credentials.
// Its absolute paths are private operator data. Do not expose it through public diagnostics.
type BackupInventory struct {
	Version      int                          `json:"version"`
	Build        string                       `json:"build"`
	DeploymentID string                       `json:"deployment_id"`
	InstanceID   string                       `json:"instance_id"`
	Roles        []BackupRole                 `json:"roles"`
	Files        []BackupFile                 `json:"files"`
	External     []productpaths.ExternalFiles `json:"external"`
	Requirements []string                     `json:"requirements"`
}
