package config

import (
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
)

// originDefault marks a setting that no layer supplied.
const originDefault = "default"

// EffectiveReport is the operator view of each catalog setting and the
// authority that supplied it. Values are redacted. Target names where a field
// save writes. Paths and Storage report where this process keeps its files and
// records, with the origin of each path. Paths.DeploymentID is the identity
// that a field save names.
type EffectiveReport struct {
	Management string             `json:"management"`
	Controller string             `json:"controller,omitempty"`
	Namespace  string             `json:"namespace,omitempty"`
	Revision   AppliedRevision    `json:"revision"`
	Target     SaveTarget         `json:"target"`
	Paths      Paths              `json:"paths"`
	Storage    []StorageLifetime  `json:"storage"`
	Settings   []EffectiveSetting `json:"settings"`
}

// EffectiveSetting reports one setting. Authority is shared, local, or
// external. Origin names the winning layer. Ignored lists the local values
// that lost, with their origin and reason.
type EffectiveSetting struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Value           string         `json:"value"`
	Scope           string         `json:"scope"`
	Authority       string         `json:"authority"`
	Origin          string         `json:"origin"`
	Ignored         []IgnoredValue `json:"ignored,omitempty"`
	DesiredRevision int64          `json:"desired_revision,omitempty"`
	AppliedRevision int64          `json:"applied_revision,omitempty"`
}

// IgnoredValue is one local value that did not win.
type IgnoredValue struct {
	Origin string `json:"origin"`
	Reason string `json:"reason"`
}

// EffectiveReport reads process memory only. It never opens the store or a file.
func (c *Config) EffectiveReport() EffectiveReport {
	report := EffectiveReport{
		Management: c.ManagementMode(), Revision: c.AppliedRevision(), Target: c.SaveTarget(),
		Paths: c.EffectivePaths(), Storage: c.StorageLifetimes(), Settings: []EffectiveSetting{},
	}
	if report.Management == ManagementExternal {
		report.Controller = ManagementExternal
	}
	if c == nil {
		return report
	}
	if c.SharedManagement() {
		report.Namespace = c.ConfigNamespace()
	}
	var resolution catalogconfig.Resolution
	if c.localCatalog != nil {
		resolution = c.localCatalog.resolution
	}
	if c.authority != nil {
		resolution = c.authority.resolution
	}
	for _, descriptor := range catalogconfig.Descriptors() {
		if strings.HasPrefix(descriptor.Name, starmapClockPrefix) || descriptor.Name == catalogconfig.AuthorityOrigin {
			continue
		}
		setting := EffectiveSetting{
			ID: descriptor.ID, Name: descriptor.Name, Scope: string(descriptor.Scope),
			Authority: ManagementLocal, Origin: originDefault,
		}
		value, present := c.Catalog.canonicalValues[descriptor.Name]
		if !present {
			value = descriptor.Default
		}
		setting.Value = redactedSetting(descriptor, value)
		if layer, found := resolution.Origins[descriptor.Name]; found {
			setting.Origin = c.layerOrigin(layer)
		}
		if shareableDescriptor(descriptor) {
			setting.Authority = report.Management
			if report.Management == ManagementShared {
				setting.DesiredRevision, setting.AppliedRevision = report.Revision.Desired, report.Revision.Applied
			}
		}
		for _, ignored := range resolution.Ignored {
			if ignored.Name == descriptor.Name {
				setting.Ignored = append(setting.Ignored, IgnoredValue{Origin: c.layerOrigin(ignored.Origin), Reason: ignored.Reason})
			}
		}
		report.Settings = append(report.Settings, setting)
	}
	return report
}

func (c *Config) layerOrigin(layer string) string {
	if c.localCatalog == nil {
		return layer
	}
	if origin, found := c.localCatalog.origins[layer]; found {
		return origin
	}
	return layer
}

func redactedSetting(descriptor catalogconfig.Descriptor, value string) string {
	switch {
	case value == "":
		return ""
	case descriptor.Sensitive:
		return redactedValue
	case descriptor.Name == catalogconfig.SourceURL:
		return redactURL(value)
	default:
		return value
	}
}
