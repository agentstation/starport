package connectors

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

// approveConnectorFixture supplies the explicit destination chosen by a transport fixture.
// Existing grants remain unchanged so mutation tests retain their original approval.
func approveConnectorFixture[T any](t testing.TB, request T) T {
	t.Helper()
	var material *credentials.Material
	var target string
	operation := catalogs.ProviderOperationChatCompletions
	method := http.MethodPost
	switch req := any(request).(type) {
	case *ChatRequest:
		material = &req.Credential
		target = req.Endpoint.URL
	case *EmbeddingsRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationEmbeddings
	case *ImagesRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationImagesGenerations
	case *SpeechRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationAudioSpeech
	case *TranscriptionRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationAudioTranscriptions
	case *RecognitionRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationDocumentsRecognition
	case *RerankRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationRerank
	case *ModerationRequest:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationModerations
	case *JobSubmission:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationVideosGenerations
	case *ProviderJobRef:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationVideosGenerations
	case *JobAssetRef:
		material = &req.Credential
		target = req.Endpoint.URL
		operation = catalogs.ProviderOperationVideosGenerations
	default:
		t.Fatalf("unsupported destination fixture %T", request)
	}
	if material.Empty() || material.HasDestinationGrant() || target == "" {
		return request
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return request
	}
	identity := credentials.DestinationIdentity{Provider: "fixture", Role: "fixture-role", Handle: material.Handle()}
	targets := []credentials.Destination{{Operation: operation, Method: method, URL: target}}
	if operation == catalogs.ProviderOperationVideosGenerations {
		job := parsed.Clone()
		job.Path = strings.TrimSuffix(job.Path, "/") + "/{job}"
		job.RawPath = ""
		content := job.Clone()
		content.Path += "/content"
		targets = append(targets, credentials.Destination{Operation: operation, Method: http.MethodGet, URL: job.String(), PathTemplate: true}, credentials.Destination{Operation: operation, Method: http.MethodDelete, URL: job.String(), PathTemplate: true}, credentials.Destination{Operation: operation, Method: http.MethodGet, URL: content.String(), PathTemplate: true})
	}
	grant, err := credentials.NewDestinationGrant(identity, material.Profile(), targets)
	require.NoError(t, err)
	*material = material.WithDestinationGrant(grant, identity, operation)
	return request
}
