package config

import "path/filepath"

// The targets of a field save. The management mode selects one target.
const (
	SaveTargetLocalFile          = "local-file"
	SaveTargetSharedRevision     = "shared-revision"
	SaveTargetExternalController = "external-controller"
)

// SaveTarget names where a field save writes before an operator saves. Path
// is the local configuration file. Unavailable explains why a local save
// refuses, and it is empty when a save can write.
type SaveTarget struct {
	Kind        string `json:"kind"`
	Path        string `json:"path,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// SaveTarget reports the field-save target of this configuration. It reads
// process memory only.
func (c *Config) SaveTarget() SaveTarget {
	switch c.ManagementMode() {
	case ManagementExternal:
		return SaveTarget{Kind: SaveTargetExternalController}
	case ManagementShared:
		return SaveTarget{Kind: SaveTargetSharedRevision}
	}
	target := SaveTarget{Kind: SaveTargetLocalFile}
	path, refusal := c.localSaveFile()
	if refusal != nil {
		target.Unavailable = refusal.Message
		return target
	}
	target.Path = path
	return target
}

// localSaveFile selects the single configuration file of a local deployment.
func (c *Config) localSaveFile() (string, *Refusal) {
	if c == nil || len(c.fileInputs) != 1 {
		return "", &Refusal{Reason: RefusalUnavailable, Message: "a local field save requires exactly one configuration file"}
	}
	path := c.fileInputs[0].location.Path
	if !filepath.IsAbs(path) {
		return "", &Refusal{Reason: RefusalUnavailable, Message: "the configuration file path is not absolute"}
	}
	return path, nil
}
