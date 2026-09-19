package config

import (
	"path/filepath"
	"slices"
	"strconv"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starmap/pkg/productpaths/policy"
	"github.com/agentstation/starmap/pkg/sources"
)

const (
	fileKindTree             = "tree"
	fileRoleBaselineRecovery = "baseline-recovery"
	fileAvailable            = "available"
	fileDisabled             = "disabled"
	filePlanned              = "planned"
)

// configurationFile records the policy that the loader used before it read any values.
type configurationFile struct {
	location productpaths.Path
	primary  bool
	access   string
	digest   [32]byte
	loaded   bool
}

// FileManifest describes selected storage without opening databases or reading credentials.
// Access policies state requirements. Only an explicit inspection observes filesystem metadata.
func (c *Config) FileManifest(version string) (productpaths.FileManifest, error) {
	p := c.EffectivePaths()
	report := productpaths.FileManifest{
		SchemaVersion: 1, Product: productpaths.Starport, BuildVersion: version,
		DeploymentID: p.DeploymentID, InstanceID: p.InstanceID,
		RelativePathBase: p.RelativePathBase, RelativePathBaseOrigin: p.RelativePathBaseOrigin,
		Roots: productpaths.Roots{},
	}
	for root, path := range map[productpaths.Root]string{
		productpaths.Config: p.ConfigDir, productpaths.Data: p.DataDir,
		productpaths.State: p.StateDir, productpaths.Cache: p.CacheDir,
	} {
		report.Roots[root] = manifestPath(p, string(root), path)
	}
	add := func(id, path, kind, availability, access, creation, recovery string, selectors ...string) {
		entry := productpaths.FileEntry{
			ID: id, Location: manifestPath(p, id, path), Kind: kind, Availability: availability,
			Creation: creation, Recovery: recovery,
			Policy: productpaths.FilePolicy{
				Selectors: selectors, Applicability: "The selected backend and operation control file creation.", Access: access,
				Retention: "Preserve until the owning storage or recovery procedure permits removal.",
				Removal:   "This report does not authorize deletion or change retention.",
			},
		}
		if kind == fileKindTree {
			entry.Patterns = []string{"**"}
		}
		report.Files = append(report.Files, entry)
	}
	add(pathRoleConfiguration, p.ConfigFile, "file", fileDisabled, policy.OwnerOnly,
		"The loader reads the selected primary file.", "Preserve configuration and required secret access.", "STARPORT_CONFIG_FILE", "STARPORT_CONFIG_DIR", "STARPORT_CONFIG_ACCESS")
	for index, input := range c.fileInputs {
		if input.primary {
			report.Files[0].Location = input.location
			report.Files[0].Availability = fileAvailable
			report.Files[0].Policy.Access = input.access
			continue
		}
		add("dotenv-"+strconv.Itoa(index), input.location.Path, "file", fileAvailable, input.access,
			"The caller explicitly selects this environment file.", "Preserve required values privately.", "explicit environment file")
		report.Files[len(report.Files)-1].Location = input.location
	}
	badger := selectedAvailability(c.Storage.Mode == storageModeBadger && !c.Storage.Badger.inMemory)
	add(pathRoleBadger, p.BadgerDir, fileKindTree, badger, policy.OwnerOnly,
		"The local KV backend opens this directory.", "Back up durable KV records and accepted catalog generations consistently. Do not share this directory between processes.", badgerPathEnvironment)
	for _, item := range []struct{ id, path, kind, origin, creation string }{
		{"setup-transaction", siblingPath(p.ConfigFile, ".starport-setup"), fileKindTree, pathRoleConfiguration, "Local setup retains its transaction and stable lock."},
		{"setup-config-publications", siblingPath(p.ConfigFile, ".record-publications"), fileKindTree, pathRoleConfiguration, "Local setup publishes configuration through private records."},
		{"setup-storage-guard", siblingPath(p.BadgerDir, ".starport-setup-"+filepath.Base(p.BadgerDir)), fileKindTree, pathRoleBadger, "Local setup and gateway startup coordinate database ownership."},
		{"setup-database-stage", siblingPath(p.BadgerDir, ""), "patterns", pathRoleBadger, "Local setup stages databases and retains interrupted rollback state."},
	} {
		add(item.id, item.path, item.kind, badger, policy.OwnerOnly, item.creation,
			"Preserve stable locks and pending records. Recovery must verify ownership before removal.", "STARPORT_CONFIG_FILE", badgerPathEnvironment)
		entry := &report.Files[len(report.Files)-1]
		entry.Location = manifestPath(p, item.origin, item.path)
		if item.kind == "patterns" {
			entry.Patterns = []string{".starport-init-*/**"}
		}
	}
	sqlite := selectedAvailability(c.Storage.SQL.Mode == sqlModeSQLite && !c.Storage.Badger.inMemory)
	add(pathRoleSQLite, p.SQLiteFile, "file", sqlite, policy.OwnerOnly,
		"The local SQL backend opens this database.", "Use a consistent SQLite backup with the matching KV records and encryption key access.", sqlitePathEnvironment)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := ""
		if p.SQLiteFile != "" {
			path = p.SQLiteFile + suffix
		}
		add(pathRoleSQLite+suffix, path, "file", sqlite, policy.OwnerOnly,
			"SQLite creates sidecars when its journal mode requires them.", "Keep database sidecars with the live database. Do not delete them during operation.", sqlitePathEnvironment)
	}
	add(pathRoleFiles, p.FilesDir, fileKindTree, selectedAvailability(c.Files.SelectedBackend() == BlobBackendFilesystem && p.FilesDir != ""), policy.OwnerOnly,
		"The filesystem blob backend stores uploaded bytes.", "Restore file records and bytes together. File retention controls normal removal.", filesPathEnvironment)
	add("local-token", p.LocalTokenFile, "file", selectedAvailability(p.LocalTokenFile != ""), policy.OwnerOnly,
		"Persistent startup creates the local administrator token. Development only reads an existing token.", "Treat this file as an administrator credential.")
	localLock := ""
	if p.LocalTokenFile != "" {
		localLock = p.LocalTokenFile + ".lock"
	}
	add("local-token-lock", localLock, "file", selectedAvailability(localLock != "" && !c.Storage.Badger.inMemory), policy.OwnerOnly,
		"Persistent token reads and writes acquire this stable lock.", "Preserve the lock while token operations can run.")
	report.Files[len(report.Files)-1].Location = manifestPath(p, "local-token", localLock)
	add("welcome-stamp", p.WelcomeStampFile, "file", selectedAvailability(!c.Catalog.StateDirectoryIsScratch()), policy.OwnerOnly,
		"Persistent onboarding records completion.", "Removal repeats the first-use notice.")
	for _, item := range []struct{ id, path, kind, creation, recovery string }{
		{pathRoleBaseline, p.BaselineDir, fileKindTree, "Persistent catalog startup exports the embedded catalog.", "Reproduce from the same binary or preserve the verified export."},
		{fileRoleBaselineRecovery, childPath(p.BaselineDir, ".starmap-baseline"), fileKindTree, "Embedded export records publication and recovery state.", "Keep locks and journals until verified recovery completes."},
		{"runtime-evidence", p.RuntimeDir, fileKindTree, "The connected catalog runtime saves private identity and acquisition evidence.", "Preserve runtime identity, layer evidence, replay floors, migration records, and locks. Accepted generations live in the selected KV backend."},
	} {
		access, err := policy.ForRole(item.id)
		if err != nil {
			return productpaths.FileManifest{}, err
		}
		add(item.id, item.path, item.kind, selectedAvailability(item.path != ""), access, item.creation, item.recovery)
		if item.id == pathRoleBaseline {
			report.Files[len(report.Files)-1].Patterns = []string{"*/manifest.json", "*/catalog.json", ".baseline-*/**"}
		}
	}
	workspaceFiles, err := productpaths.WorkspaceFiles(manifestPath(p, "workspace", c.Catalog.WorkspacePath), "STARPORT_CATALOG_WORKSPACE_PATH")
	if err != nil {
		return productpaths.FileManifest{}, err
	}
	report.Files = append(report.Files, workspaceFiles...)
	if c.Catalog.Source == CatalogSourceFile {
		add("source-file", c.Catalog.SourceURL, "file", fileAvailable, policy.DeploymentControlled,
			"The operator selects a file catalog source.", "Preserve the source catalog and its access policy.", "STARPORT_CATALOG_SOURCE_URL")
	}
	for _, item := range []struct{ id, path, access string }{
		{"tls-certificate", c.Security.TLSCertPath, policy.DeploymentControlled}, {"tls-key", c.Security.TLSKeyPath, policy.OwnerOnly},
	} {
		add(item.id, item.path, "file", selectedAvailability(c.Security.EnableTLS && item.path != ""), item.access,
			"The operator supplies TLS material.", "Renew through the deployment certificate procedure.")
	}
	add("logs", c.Logging.FilePath, "file", filePlanned, policy.OwnerOnly,
		"File logging has no implemented application writer.", "Current application logging uses streams.", "STARPORT_LOGGING_FILE_PATH")
	selection := make(map[string]string)
	if value, present := c.Catalog.canonicalValues[catalogconfig.AcquisitionSources]; present {
		selection[catalogconfig.AcquisitionSources] = value
	}
	parsed, err := catalogconfig.Parse(selection)
	if err != nil {
		return productpaths.FileManifest{}, err
	}
	ids, explicit := parsed.AcquisitionSourceSelection()
	if !explicit {
		ids = []sources.ID{sources.LocalCatalogID, sources.ModelsDevHTTPID}
	}
	for _, source := range []struct {
		role, path string
		id         sources.ID
	}{
		{"source-http", childPath(p.CacheDir, "models.dev"), sources.ModelsDevHTTPID},
		{"source-checkout", childPath(p.CacheDir, "sources", "models.dev-git"), sources.ModelsDevGitID},
	} {
		access, err := policy.ForRole(source.role)
		if err != nil {
			return productpaths.FileManifest{}, err
		}
		selected := p.CacheDir != "" && slices.Contains(ids, source.id) && c.Catalog.SourceStartupPolicy != CatalogStartupRequireAuthority
		add(source.role, source.path, fileKindTree, selectedAvailability(selected), access,
			"Permitted explicit or scheduled source acquisition creates this cache.", "Rebuild only through permitted source access. Preserve accepted catalog evidence in runtime state.", "STARPORT_CACHE_DIR", "STARPORT_CATALOG_ACQUISITION_SOURCES")
	}
	c.addExternalStorage(&report)
	return report, nil
}

func (c *Config) addExternalStorage(report *productpaths.FileManifest) {
	add := func(id, selection, recovery string) {
		report.External = append(report.External, productpaths.ExternalFiles{
			ID: id, Selection: selection, Recovery: recovery,
			Policy: productpaths.FilePolicy{Access: policy.ExternalSystem,
				Applicability: "Only the explicitly selected service owns these records.",
				Retention:     "The service and record owner control retention.", Removal: "This report does not authorize deletion."},
		})
	}
	if c.Storage.Mode == storageModeValkey {
		add("kv", storageModeValkey, "Preserve durable records and accepted catalog generations. Do not fall back to local Badger on an outage.")
	} else if c.Storage.Badger.inMemory {
		add("kv", "process-memory", "Development records expire with the process.")
	}
	if c.Storage.SQL.Mode != sqlModeSQLite {
		add("sql", c.Storage.SQL.Mode, "Restore SQL and KV records consistently. Do not fall back to SQLite on an outage.")
	} else if c.Storage.Badger.inMemory {
		add("sql", "process-memory", "Development records expire with the process.")
	}
	if c.Files.SelectedBackend() == BlobBackendObjectStore {
		add("blobs", BlobBackendObjectStore, "Restore object bytes and file records together. Endpoints and credentials are omitted.")
	}
}

func selectedAvailability(selected bool) string {
	if selected {
		return fileAvailable
	}
	return fileDisabled
}

func childPath(parent string, parts ...string) string {
	if parent == "" {
		return ""
	}
	return filepath.Join(append([]string{parent}, parts...)...)
}

func manifestPath(paths Paths, role, path string) productpaths.Path {
	originRole := role
	switch role {
	case fileRoleBaselineRecovery:
		originRole = pathRoleBaseline
	case "runtime-evidence":
		originRole = pathRoleRuntime
	case "sqlite-wal", "sqlite-shm", "sqlite-journal":
		originRole = pathRoleSQLite
	}
	selected, ok := paths.Origins[originRole]
	if !ok {
		switch role {
		case pathRoleBaseline, fileRoleBaselineRecovery, "welcome-stamp":
			selected.Origin = "derived:data"
		case "source-http", "source-checkout":
			selected.Origin = "derived:cache"
		default:
			selected.Origin = pathOriginDefault
		}
	}
	selected.Path = path
	return selected
}

func siblingPath(selected, name string) string {
	if selected == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(selected), name)
}
