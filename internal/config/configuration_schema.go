package config

import (
	"maps"
	"slices"
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
)

// MutabilityMigrationOnly marks a setting that only a management migration
// changes. A field save refuses it.
const MutabilityMigrationOnly = "migration-only"

// The schema entry of the management mode.
const (
	managementSettingID  = "config.management"
	managementSettingKey = "config_management"
)

// SchemaSetting describes one configuration setting for an operator surface.
// Key is the field-save key. Environment is the Starport variable name.
type SchemaSetting struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Environment   string   `json:"environment"`
	Type          string   `json:"type"`
	Scope         string   `json:"scope"`
	Mutability    string   `json:"mutability"`
	Sensitive     bool     `json:"sensitive"`
	Applicability []string `json:"applicability"`
}

// ConfigurationSchema projects the Starmap catalog descriptors and the
// management mode. It holds no values.
func ConfigurationSchema() []SchemaSetting {
	var settings []SchemaSetting
	for _, descriptor := range catalogconfig.Descriptors() {
		if strings.HasPrefix(descriptor.Name, starmapClockPrefix) || descriptor.Name == catalogconfig.AuthorityOrigin {
			continue
		}
		settings = append(settings, SchemaSetting{
			ID: descriptor.ID, Key: descriptor.Key, Environment: catalogEnvironmentName(descriptor.Name),
			Type: string(descriptor.Type), Scope: string(descriptor.Scope), Mutability: descriptor.Mutability,
			Sensitive: descriptor.Sensitive, Applicability: slices.Clone(descriptor.Applicability),
		})
	}
	return append(settings, SchemaSetting{
		ID: managementSettingID, Key: managementSettingKey, Environment: managementEnvironment,
		Type: string(catalogconfig.StringValue), Scope: string(catalogconfig.NodeScope),
		Mutability: MutabilityMigrationOnly, Applicability: []string{"all"},
	})
}

// FieldEdit is one resolved field edit. A nil value removes the setting from
// the configuration authority.
type FieldEdit struct {
	// Name is the canonical Starmap setting name.
	Name string
	// Key is the field-save key that the receipt and the audit record name.
	Key   string
	Value *string
}

// ResolveFieldEdits maps field-save keys or Starport variable names to the
// deployment-scope catalog settings that a save can change. It refuses an
// empty edit set, an unknown key, the management mode, and a node-scope
// setting. The result is in key order.
func ResolveFieldEdits(edits map[string]*string) ([]FieldEdit, error) {
	if len(edits) == 0 {
		return nil, &Refusal{Reason: RefusalInvalidEdit, Message: "a configuration save requires at least one edit"}
	}
	descriptors := catalogconfig.Descriptors()
	resolved := make([]FieldEdit, 0, len(edits))
	seen := make(map[string]bool, len(edits))
	for _, key := range slices.Sorted(maps.Keys(edits)) {
		if key == managementSettingKey || key == managementEnvironment {
			return nil, &Refusal{
				Reason: RefusalMigrationBoundary, Setting: managementSettingKey,
				Message: "a field save cannot change STARPORT_CONFIG_MANAGEMENT. Run starport config migrate to change the management mode",
			}
		}
		index := slices.IndexFunc(descriptors, func(descriptor catalogconfig.Descriptor) bool {
			return descriptor.Key == key || catalogEnvironmentName(descriptor.Name) == key
		})
		if index < 0 || strings.HasPrefix(descriptors[index].Name, starmapClockPrefix) || descriptors[index].Name == catalogconfig.AuthorityOrigin {
			return nil, &Refusal{Reason: RefusalInvalidEdit, Setting: safeSettingKey(key), Message: "the configuration schema has no editable setting with this key"}
		}
		descriptor := descriptors[index]
		if !shareableDescriptor(descriptor) {
			return nil, &Refusal{
				Reason: RefusalInvalidEdit, Setting: descriptor.Key,
				Message: "a field save changes deployment-scope settings only. Set node-scope settings in the environment of each process",
			}
		}
		if seen[descriptor.Name] {
			return nil, &Refusal{Reason: RefusalInvalidEdit, Setting: descriptor.Key, Message: "two edit keys name the same setting"}
		}
		seen[descriptor.Name] = true
		resolved = append(resolved, FieldEdit{Name: descriptor.Name, Key: descriptor.Key, Value: edits[key]})
	}
	slices.SortFunc(resolved, func(a, b FieldEdit) int { return strings.Compare(a.Key, b.Key) })
	return resolved, nil
}

// FieldEditKeys returns the keys of resolved edits.
func FieldEditKeys(edits []FieldEdit) []string {
	keys := make([]string, len(edits))
	for index, edit := range edits {
		keys[index] = edit.Key
	}
	return keys
}

// ApplyFieldEdits returns a copy of canonical values with the edits applied.
func ApplyFieldEdits(values map[string]string, edits []FieldEdit) map[string]string {
	next := make(map[string]string, len(values)+len(edits))
	for name, value := range values {
		next[name] = value
	}
	for _, edit := range edits {
		if edit.Value == nil {
			delete(next, edit.Name)
			continue
		}
		next[edit.Name] = *edit.Value
	}
	return next
}

// ChangedSettingKeys returns the field-save keys of the canonical settings
// whose values differ between two value sets, in key order.
func ChangedSettingKeys(before, after map[string]string) []string {
	var keys []string
	for _, descriptor := range catalogconfig.Descriptors() {
		previous, wasSet := before[descriptor.Name]
		next, isSet := after[descriptor.Name]
		if wasSet != isSet || previous != next {
			keys = append(keys, descriptor.Key)
		}
	}
	slices.Sort(keys)
	return keys
}

// safeSettingKey returns an unknown key for a refusal only when it has the
// form of a setting name. Any other text could be a misplaced value.
func safeSettingKey(key string) string {
	if key == "" || len(key) > 128 {
		return ""
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '.' {
			return ""
		}
	}
	return key
}
