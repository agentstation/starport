package config

import (
	"reflect"
	"regexp"
	"strings"
	"time"
)

const redactedValue = "<redacted>"

var (
	wordBoundary    = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
	initialBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	durationType    = reflect.TypeOf(time.Duration(0))
)

// Redacted returns an inspectable configuration tree without secret values.
func Redacted(cfg *Config) map[string]any {
	if cfg == nil {
		return nil
	}
	value, _ := redactValue(reflect.ValueOf(*cfg), reflect.StructField{})
	redacted, _ := value.(map[string]any)
	providerValues := make(map[string]any, len(cfg.Providers.Entries()))
	for _, entry := range cfg.Providers.Entries() {
		provider, _ := redactValue(reflect.ValueOf(entry.Config), reflect.StructField{})
		providerValues[string(entry.ProviderID)] = provider
	}
	redacted["providers"] = providerValues
	return redacted
}

func redactValue(value reflect.Value, field reflect.StructField) (any, bool) {
	if field.Tag.Get("secret") == "true" {
		if value.Kind() == reflect.String && value.String() == "" {
			return "", true
		}
		return redactedValue, true
	}
	if value.Type() == durationType {
		return time.Duration(value.Int()).String(), true
	}
	if field.Tag.Get("redact") == "url" && value.Kind() == reflect.String {
		return redactURL(value.String()), true
	}
	switch value.Kind() {
	case reflect.Struct:
		result := make(map[string]any, value.NumField())
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			childField := valueType.Field(index)
			if !childField.IsExported() {
				continue
			}
			child, include := redactValue(value.Field(index), childField)
			if include {
				result[snakeCase(childField.Name)] = child
			}
		}
		return result, true
	case reflect.String:
		return value.String(), true
	case reflect.Bool:
		return value.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	default:
		return nil, false
	}
}

func redactURL(value string) string {
	if value == "" {
		return ""
	}
	return redactedValue
}

func snakeCase(value string) string {
	value = wordBoundary.ReplaceAllString(value, `${1}_${2}`)
	value = initialBoundary.ReplaceAllString(value, `${1}_${2}`)
	return strings.ToLower(value)
}
