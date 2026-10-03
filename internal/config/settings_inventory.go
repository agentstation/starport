package config

import (
	"reflect"
	"slices"
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productpaths"
)

// Default sources of an environment setting in the settings inventory.
const (
	defaultSourceTag          = "tag"
	defaultSourceDescriptor   = "catalog-descriptor"
	defaultSourcePlatformPath = "platform-path"
)

// Redaction kinds that the inventory reports. They follow the inspection
// conventions: a secret tag hides the whole value, and a url redaction hides
// a connection string.
const (
	redactionValue = "value"
	redactionURL   = "url"
)

// settingsInventory is the generated settings reference. It holds settings
// metadata only, never a configured value.
type settingsInventory struct {
	Generator   string                   `json:"generator"`
	Schema      []schemaInventorySetting `json:"schema"`
	Environment []environmentSetting     `json:"environment"`
}

// schemaInventorySetting is one ConfigurationSchema entry with the Starmap
// descriptor facts that the schema projection leaves out.
type schemaInventorySetting struct {
	SchemaSetting  `json:",inline"`
	Origin         string   `json:"origin"`
	Default        string   `json:"default,omitempty"`
	DefaultMeaning string   `json:"default_meaning,omitempty"`
	Unit           string   `json:"unit,omitempty"`
	AllowedValues  []string `json:"allowed_values,omitempty"`
	Description    string   `json:"description,omitempty"`
}

// environmentSetting is one Starport variable that a Config env tag names.
type environmentSetting struct {
	Name          string `json:"name"`
	Section       string `json:"section"`
	Field         string `json:"field"`
	Type          string `json:"type"`
	Default       string `json:"default,omitempty"`
	DefaultSource string `json:"default_source,omitempty"`
	Secret        bool   `json:"secret"`
	Redaction     string `json:"redaction,omitempty"`
	SchemaID      string `json:"schema_id,omitempty"`
}

// newSettingsInventory projects the configuration schema and the Config env
// tags. It reads no environment and no file.
func newSettingsInventory() settingsInventory {
	descriptors := make(map[string]catalogconfig.Descriptor)
	for _, descriptor := range catalogconfig.Descriptors() {
		descriptors[descriptor.ID] = descriptor
	}
	inventory := settingsInventory{Generator: referenceGenerator}
	schemaIDs := make(map[string]string)
	for _, setting := range ConfigurationSchema() {
		entry := schemaInventorySetting{SchemaSetting: setting, Origin: string(productpaths.Starport)}
		if descriptor, ok := descriptors[setting.ID]; ok {
			entry.Origin = string(productpaths.Starmap)
			entry.Default, entry.DefaultMeaning, entry.Unit = descriptor.Default, descriptor.DefaultMeaning, descriptor.Unit
			entry.AllowedValues, entry.Description = slices.Clone(descriptor.AllowedValues), descriptor.Description
		}
		inventory.Schema = append(inventory.Schema, entry)
		schemaIDs[setting.Environment] = setting.ID
	}
	// The loader presets the storage paths from the resolved roots before it
	// decodes the environment. A preset field reports a platform path default.
	preset := "preset"
	presets := reflect.ValueOf(defaultConfig(Paths{
		BadgerDir: preset, SQLiteFile: preset, FilesDir: preset, LocalTokenFile: preset,
	})).Elem()
	walkEnvironmentFields(reflect.TypeFor[Config](), presets, NewLoader().prefix, "", "", &inventory.Environment)
	for index := range inventory.Environment {
		setting := &inventory.Environment[index]
		setting.SchemaID = schemaIDs[setting.Name]
		if setting.Default != "" || setting.DefaultSource != "" {
			continue
		}
		if descriptor, ok := descriptors[setting.SchemaID]; ok && descriptor.Default != "" {
			setting.Default, setting.DefaultSource = descriptor.Default, defaultSourceDescriptor
		}
	}
	return inventory
}

// walkEnvironmentFields follows the go-envconfig traversal: it skips
// unexported and untagged leaves, and it descends into a struct without its
// own key under the struct's prefix.
func walkEnvironmentFields(structType reflect.Type, presets reflect.Value, prefix, section, path string, settings *[]environmentSetting) {
	for index := range structType.NumField() {
		field := structType.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("env")
		key, fieldPrefix, fallback := parseEnvironmentTag(tag)
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		fieldSection := section
		if fieldSection == "" {
			fieldSection = snakeCase(field.Name)
		}
		fieldPath := field.Name
		if path != "" {
			fieldPath = path + "." + field.Name
		}
		preset := reflect.Value{}
		if presets.IsValid() && presets.Field(index).Kind() != reflect.Pointer {
			preset = presets.Field(index)
		}
		if fieldType.Kind() == reflect.Struct && key == "" {
			walkEnvironmentFields(fieldType, preset, prefix+fieldPrefix, fieldSection, fieldPath, settings)
			continue
		}
		if tag == "" {
			continue
		}
		setting := environmentSetting{
			Name: prefix + key, Section: fieldSection, Field: fieldPath, Type: field.Type.String(), Default: fallback,
		}
		switch {
		case fallback != "":
			setting.DefaultSource = defaultSourceTag
		case preset.IsValid() && !preset.IsZero():
			setting.DefaultSource = defaultSourcePlatformPath
		}
		switch {
		case secretField(field):
			setting.Secret, setting.Redaction = true, redactionValue
		case urlRedactedField(field):
			setting.Secret, setting.Redaction = true, redactionURL
		}
		*settings = append(*settings, setting)
	}
}

// parseEnvironmentTag returns the key, the prefix option, and the default
// option of a go-envconfig tag. A default option holds the rest of the tag.
func parseEnvironmentTag(tag string) (key, prefix, fallback string) {
	key, options, _ := strings.Cut(tag, ",")
	for options != "" {
		var option string
		if value, ok := strings.CutPrefix(options, "default="); ok {
			return strings.TrimSpace(key), prefix, value
		}
		option, options, _ = strings.Cut(options, ",")
		if value, ok := strings.CutPrefix(strings.TrimSpace(option), "prefix="); ok {
			prefix = value
		}
	}
	return strings.TrimSpace(key), prefix, ""
}
