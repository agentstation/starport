package connectors

import (
	"fmt"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func selectedEndpoint(
	endpoint InferenceEndpoint,
	expected catalogs.EndpointType,
) (string, error) {
	if endpoint.Type != expected {
		return "", fmt.Errorf("endpoint protocol %q is not supported; want %q", endpoint.Type, expected)
	}
	if strings.TrimSpace(endpoint.URL) == "" {
		return "", fmt.Errorf("endpoint URL is required")
	}
	return endpoint.URL, nil
}
