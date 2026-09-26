package catalog

import (
	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/productpaths"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
)

// metadataCollector binds non-provider acquisition to this host's filesystem roots.
// Runtime source selection, network policy, and authority still govern every acquisition.
func (s Settings) metadataCollector() (*acquisition.SourceAcquirer, error) {
	var directories productpaths.SourceDirectories
	var err error
	if s.SourceCacheDirectory == "" {
		directories, err = productpaths.DefaultSourceDirectories(productpaths.Starport)
	} else {
		directories, err = productpaths.SourceDirectoriesAt(s.SourceCacheDirectory)
	}
	if err != nil {
		return nil, err
	}
	options := []pkgsync.Option{
		pkgsync.WithCatalogPath(s.WorkspacePath),
		pkgsync.WithSourceDirectories(directories),
		pkgsync.WithSkipDepPrompts(true),
	}
	if err := pkgsync.Defaults().Apply(options...).ValidateFilesystemLayout(); err != nil {
		return nil, err
	}
	return acquisition.NewSourceAcquirer(options...)
}
