package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

func TestRedactedNeverReturnsSecrets(t *testing.T) {
	secrets := []string{
		"storage-password-value", "provider-api-key-value",
		"master-key-value", "jwt-secret-value", "query-token-value",
		"userinfo-password-value", "fragment-secret-value",
		"remote-catalog-api-key-value",
	}
	cfg := &Config{
		Storage: StorageConfig{Valkey: ValkeyConfig{
			URL:      "rediss://user:userinfo-password-value@example.com:6379/0?token=query-token-value#fragment-secret-value",
			Password: secrets[0],
		}},
		Providers: ProvidersConfig{"openai": {
			Material: credentials.NewMaterial(
				catalogs.ProviderCredentialProfile{
					ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
				},
				map[catalogs.ProviderCredentialFieldID]string{"api-key": secrets[1]},
				credentials.MaterialMetadata{Version: "opaque"},
			),
			BaseURL: "https://example.com/v1?api_key=query-token-value",
		}},
		Catalog: CatalogConfig{
			SourceURL:    "https://catalog.example/api/v1?token=query-token-value",
			SourceAPIKey: secrets[7],
		},
		Security: SecurityConfig{MasterKey: secrets[2], JWTSecret: secrets[3]},
	}
	encoded, err := json.Marshal(Redacted(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("redacted configuration contains secret %q", secret)
		}
	}
	view := Redacted(cfg)
	providers := view["providers"].(map[string]any)
	openAI := providers["openai"].(map[string]any)
	security := view["security"].(map[string]any)
	if security["master_key"] != redactedValue {
		t.Errorf("redacted master key = %#v", security["master_key"])
	}
	if material, found := openAI["material"]; !found || len(material.(map[string]any)) != 0 {
		t.Errorf("redacted material = %#v", material)
	}
	storage := view["storage"].(map[string]any)
	valkey := storage["valkey"].(map[string]any)
	catalog := view["catalog"].(map[string]any)
	if openAI["base_url"] != redactedValue ||
		valkey["url"] != redactedValue ||
		catalog["source_url"] != redactedValue {
		t.Errorf(
			"redacted URLs = %#v, %#v, %#v",
			openAI["base_url"],
			valkey["url"],
			catalog["remote_url"],
		)
	}
	if _, found := view["console"]; !found {
		t.Fatal("console key is missing")
	}
}

func TestConfigurationSecretFieldsDeclareRedaction(t *testing.T) {
	assertSecretTags(t, reflect.TypeOf(Config{}), "")
}

func TestRedactedUsesReadableDurationAndAcronymKeys(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{ReadTimeout: 30},
		Console:  ConsoleConfig{Enabled: true},
		Security: SecurityConfig{TLSCertPath: "/cert.pem"},
	}
	view := Redacted(cfg)
	server := view["server"].(map[string]any)
	if server["read_timeout"] != "30ns" {
		t.Errorf("read timeout = %#v", server["read_timeout"])
	}
	if _, found := view["console"]; !found {
		t.Fatal("console key is missing")
	}
	security := view["security"].(map[string]any)
	if security["tls_cert_path"] != "/cert.pem" {
		t.Errorf("TLS path = %#v", security["tls_cert_path"])
	}
}

func assertSecretTags(t *testing.T, value reflect.Type, path string) {
	t.Helper()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		name := field.Name
		fieldPath := name
		if path != "" {
			fieldPath = path + "." + name
		}
		if field.Type.Kind() == reflect.Struct && field.Type != durationType {
			assertSecretTags(t, field.Type, fieldPath)
			continue
		}
		sensitive := strings.HasSuffix(name, "APIKey") || strings.Contains(name, "Password") ||
			strings.Contains(name, "MasterKey") || strings.Contains(name, "JWTSecret")
		if sensitive && field.Tag.Get("secret") != "true" {
			t.Errorf("configuration secret %s does not declare redaction", fieldPath)
		}
		if strings.HasSuffix(name, "URL") && field.Tag.Get("redact") != "url" {
			t.Errorf("configuration URL %s does not declare URL redaction", fieldPath)
		}
	}
}
