package credentials

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// ReferenceBackend identifies one credential source primitive.
type ReferenceBackend string

const (
	// ReferenceBackendEnvironment reads one exact environment variable.
	ReferenceBackendEnvironment ReferenceBackend = "env"
	// ReferenceBackendFile reads one exact local file.
	ReferenceBackendFile ReferenceBackend = "file"
	// ReferenceBackendGCPStore reads Google Cloud Secret Manager.
	ReferenceBackendGCPStore ReferenceBackend = "gcp-secret-manager"
	// ReferenceBackendAzureVault reads Azure Key Vault.
	ReferenceBackendAzureVault ReferenceBackend = "azure-key-vault"
	// ReferenceBackendAWSStore reads AWS Secrets Manager.
	ReferenceBackendAWSStore ReferenceBackend = "aws-secrets-manager"
	// ReferenceBackendVault reads HashiCorp Vault KV v2.
	ReferenceBackendVault ReferenceBackend = "vault"
	// ReferenceBackendOpenBao reads OpenBao KV v2.
	ReferenceBackendOpenBao ReferenceBackend = "openbao"
)

var (
	referenceBackendPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	credentialEnvironmentPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// Reference identifies one operator-selected source without containing source
// authentication values.
type Reference struct {
	backend  ReferenceBackend
	resource string
	field    string
	version  string
}

// Backend returns the selected source primitive.
func (r Reference) Backend() ReferenceBackend { return r.backend }

// Resource returns the operator-selected source resource.
func (r Reference) Resource() string { return r.resource }

// Field returns the selected structured-source field.
func (r Reference) Field() string { return r.field }

// Version returns the selected source version.
func (r Reference) Version() string { return r.version }

// ParseReference parses backend:resource?version=VERSION#field syntax.
func ParseReference(value string) (Reference, error) {
	backendValue, remainder, found := strings.Cut(value, ":")
	backend := ReferenceBackend(backendValue)
	if !found || !referenceBackendPattern.MatchString(backendValue) {
		return Reference{}, referenceValidationError("backend", "must be a lowercase kebab-case ID")
	}
	resourceAndQuery, field, hasField := strings.Cut(remainder, "#")
	resource, rawQuery, hasQuery := strings.Cut(resourceAndQuery, "?")
	if resource == "" {
		return Reference{}, referenceValidationError("resource", "is required")
	}
	if hasField && field == "" {
		return Reference{}, referenceValidationError("field", "must be nonempty")
	}
	if strings.Contains(field, "#") {
		return Reference{}, referenceValidationError("field", "must not contain #")
	}
	version := ""
	if hasQuery {
		if rawQuery == "" {
			return Reference{}, referenceValidationError("query", "must be nonempty")
		}
		query, err := url.ParseQuery(rawQuery)
		if err != nil {
			return Reference{}, referenceValidationError("query", "is invalid")
		}
		for key := range query {
			if key != "version" {
				return Reference{}, referenceValidationError("query", "contains an unsupported parameter")
			}
		}
		versions := query["version"]
		if len(versions) != 1 || versions[0] == "" {
			return Reference{}, referenceValidationError("version", "must contain one nonempty value")
		}
		version = versions[0]
	}
	return Reference{backend: backend, resource: resource, field: field, version: version}, nil
}

func referenceValidationError(field, message string) error {
	return &ReferenceError{Field: field, Message: message}
}

// ReferenceError reports an invalid source reference without its value.
type ReferenceError struct {
	Field   string
	Message string
}

func (e *ReferenceError) Error() string {
	return fmt.Sprintf("credential reference %s %s", e.Field, e.Message)
}

// CredentialFieldKey identifies one provider field source policy.
type CredentialFieldKey struct {
	ProviderID catalogs.ProviderID
	FieldID    catalogs.ProviderCredentialFieldID
}

// ReferencePolicy selects an explicit source and an optional not-configured
// fallback to ambient discovery.
type ReferencePolicy struct {
	Reference       Reference
	FallbackAmbient bool
}
