package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

type mockAuthors struct {
	unsupportedMedia
	authors *proxy.AuthorsResponse
	author  *proxy.AuthorInfo
	err     error
}

func (m *mockAuthors) ProcessChatCompletion(context.Context, *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthors) ProcessChatCompletionStream(context.Context, *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthors) ProcessEmbeddings(context.Context, *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthors) ListModels(context.Context) (*proxy.ModelsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthors) ListProviders(context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuthors) GetLogo(context.Context, view.LogoKind, string) ([]byte, error) {
	return nil, &proxy.ProviderError{Code: "not_found", Message: "Logo not found"}
}

func (m *mockAuthors) ListAuthors(context.Context) (*proxy.AuthorsResponse, error) {
	return m.authors, m.err
}

func (m *mockAuthors) GetAuthor(context.Context, string) (*proxy.AuthorInfo, error) {
	return m.author, m.err
}

func (m *mockAuthors) GetModelEndpoints(context.Context, string) (*proxy.ModelEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

func authorFixture() *proxy.AuthorInfo {
	return &proxy.AuthorInfo{
		ID: "anthropic", Name: "Anthropic",
		Website: "https://www.anthropic.com",
		Models:  []string{"anthropic/claude-fable-5"},
	}
}

func TestAuthorsControllerList(t *testing.T) {
	controller := NewAuthorsController(&mockAuthors{
		authors: &proxy.AuthorsResponse{Authors: []proxy.AuthorInfo{*authorFixture()}},
	})
	recorder := httptest.NewRecorder()
	controller.List(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/authors", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	authors := response["authors"].([]any)
	require.Len(t, authors, 1)
	author := authors[0].(map[string]any)
	require.Equal(t, "anthropic", author["id"])
	require.Equal(t, "Anthropic", author["name"])
	require.Equal(t, []any{"anthropic/claude-fable-5"}, author["models"])
}

func TestAuthorsControllerGet(t *testing.T) {
	controller := NewAuthorsController(&mockAuthors{author: authorFixture()})
	router := chi.NewRouter()
	router.Get("/api/v1/authors/{author}", controller.Get)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/authors/anthropic", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"id":"anthropic"`)
	require.Contains(t, recorder.Body.String(), `"website":"https://www.anthropic.com"`)
}

func TestAuthorsControllerGetUnknownIs404(t *testing.T) {
	controller := NewAuthorsController(&mockAuthors{
		err: &proxy.ProviderError{Code: "not_found", Message: "Author not found"},
	})
	router := chi.NewRouter()
	router.Get("/api/v1/authors/{author}", controller.Get)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/authors/no-such-author", nil))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Author not found")
}
