package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

func testAPIMaterial(value string) credentials.Material {
	return testPlacedMaterial(
		catalogs.ProviderAuthenticationAPIKey,
		"Authorization",
		catalogs.ProviderCredentialSchemeBearer,
		value,
	)
}

func testAnthropicMaterial(value string) credentials.Material {
	return testPlacedMaterial(
		catalogs.ProviderAuthenticationAPIKey,
		"x-api-key",
		catalogs.ProviderCredentialSchemeDirect,
		value,
	)
}

func testGoogleMaterial(value string) credentials.Material {
	return testPlacedMaterial(
		catalogs.ProviderAuthenticationAPIKey,
		"x-goog-api-key",
		catalogs.ProviderCredentialSchemeDirect,
		value,
	)
}

func testPlacedMaterial(
	primitive catalogs.ProviderAuthenticationPrimitive,
	header string,
	scheme catalogs.ProviderCredentialScheme,
	value string,
) credentials.Material {
	profile := catalogs.ProviderCredentialProfile{
		ID: "test-profile", Primitive: primitive,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: header, Scheme: scheme,
		}},
	}
	return credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"api-key": value},
		credentials.MaterialMetadata{Version: "test"},
	)
}

func testGoogleDefaultMaterial(value string) credentials.Material {
	profile := catalogs.ProviderCredentialProfile{
		ID: "workload-identity", Primitive: catalogs.ProviderAuthenticationGoogleDefault,
		Fields: []catalogs.ProviderCredentialFieldID{"access-token"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "access-token", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
	}
	return credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"access-token": value},
		credentials.MaterialMetadata{Version: "test"},
	)
}

func testNoAuthenticationMaterial() credentials.Material {
	return credentials.NewMaterial(
		catalogs.ProviderCredentialProfile{
			ID: "public", Primitive: catalogs.ProviderAuthenticationNone,
		},
		nil,
		credentials.MaterialMetadata{Version: "test"},
	)
}

func TestConnectorsStoreNoCredentialMaterial(t *testing.T) {
	connectorTypes := []reflect.Type{
		reflect.TypeFor[OpenAIConnector](),
		reflect.TypeFor[AnthropicConnector](),
		reflect.TypeFor[GoogleAIStudioConnector](),
		reflect.TypeFor[VertexAIConnector](),
		reflect.TypeFor[OllamaConnector](),
		reflect.TypeFor[providerConnector](),
	}
	materialType := reflect.TypeFor[credentials.Material]()
	materialSourceType := reflect.TypeFor[credentials.MaterialSource]()
	for _, connectorType := range connectorTypes {
		for index := range connectorType.NumField() {
			field := connectorType.Field(index)
			if field.Type == materialType || field.Type.Implements(materialSourceType) ||
				stringsContainFold(field.Name, "credential") || stringsContainFold(field.Name, "apiKey") {
				t.Fatalf("connector %s retains credential field %s (%s)", connectorType, field.Name, field.Type)
			}
		}
	}
}

func TestConcurrentRequestsUseOnlySelectedCredentialMaterial(t *testing.T) {
	const requestCount = 64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if got, want := request.Header.Get("Authorization"), "Bearer token-"+body.Model; got != want {
			t.Errorf("model %s Authorization = %q, want %q", body.Model, got, want)
		}
		_ = json.NewEncoder(writer).Encode(ChatResponse{
			ID: "response", Object: objectChatCompletion, Model: body.Model,
			Choices: []Choice{{Message: Message{Role: RoleAssistant, Content: "ok"}}},
		})
	}))
	defer server.Close()

	connector, err := newOpenAIConnector("acme", "acme", ProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	defer connector.Close()

	var group sync.WaitGroup
	for index := range requestCount {
		group.Add(1)
		go func() {
			defer group.Done()
			model := fmt.Sprintf("%d", index)
			_, requestErr := connector.Chat(context.Background(), &ChatRequest{
				Model: model, Messages: []Message{{Role: RoleUser, Content: "hello"}},
				Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL},
				Credential: testAPIMaterial("token-" + model),
			})
			if requestErr != nil {
				t.Errorf("chat %s: %v", model, requestErr)
			}
		}()
	}
	group.Wait()
}

func stringsContainFold(value, target string) bool {
	return len(value) >= len(target) &&
		strings.Contains(strings.ToLower(value), strings.ToLower(target))
}
