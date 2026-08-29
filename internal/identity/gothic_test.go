package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/markbates/goth"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// fakeProvider implements goth.Provider so the OAuth dance can run entirely
// in-process: Begin issues a consent URL carrying the state, and the test
// plays the provider sending the browser back with that state.
type fakeProvider struct {
	name    string
	profile goth.User
}

type fakeSession struct {
	AuthURL string
}

func (s *fakeSession) GetAuthURL() (string, error) { return s.AuthURL, nil }
func (s *fakeSession) Marshal() string {
	data, _ := json.Marshal(s)
	return string(data)
}

func (s *fakeSession) Authorize(goth.Provider, goth.Params) (string, error) {
	return "access-token", nil
}

func (p *fakeProvider) Name() string                { return p.name }
func (p *fakeProvider) SetName(name string)         { p.name = name }
func (p *fakeProvider) Debug(bool)                  {}
func (p *fakeProvider) RefreshTokenAvailable() bool { return false }
func (p *fakeProvider) RefreshToken(string) (*oauth2.Token, error) {
	return nil, errors.New("no refresh")
}

func (p *fakeProvider) BeginAuth(state string) (goth.Session, error) {
	return &fakeSession{AuthURL: "https://provider.test/consent?state=" + state}, nil
}

func (p *fakeProvider) UnmarshalSession(data string) (goth.Session, error) {
	session := &fakeSession{}
	return session, json.Unmarshal([]byte(data), session)
}

func (p *fakeProvider) FetchUser(goth.Session) (goth.User, error) {
	profile := p.profile
	profile.Provider = p.name
	return profile, nil
}

// beginAndCallback drives one full dance: Begin against the acquisition
// path, then the provider's redirect back, carrying the state cookie the
// begin response set. It returns the claim Complete issued.
func beginAndCallback(t *testing.T, path *Gothic, provider string) string {
	t.Helper()

	begin := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/identity/"+provider, nil)
	require.NoError(t, path.Begin(begin, request, provider))
	require.Equal(t, http.StatusTemporaryRedirect, begin.Code)

	consent, err := url.Parse(begin.Header().Get("Location"))
	require.NoError(t, err)
	state := consent.Query().Get("state")
	require.NotEmpty(t, state)

	callback := httptest.NewRequest(http.MethodGet,
		CallbackPath(provider)+"?state="+url.QueryEscape(state)+"&code=granted", nil)
	for _, cookie := range begin.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	complete := httptest.NewRecorder()
	claim, err := path.Complete(complete, callback, provider)
	require.NoError(t, err)
	require.NotEmpty(t, claim)
	return claim
}

func newTestGothic(t *testing.T, provider *fakeProvider) *Gothic {
	t.Helper()
	repositories := newTestRepositories(t)
	goth.UseProviders(provider)
	t.Cleanup(func() { delete(goth.GetProviders(), provider.Name()) })
	path, err := newGothic(repositories.Users, []string{provider.Name()})
	require.NoError(t, err)
	return path
}

// TestOAuthSignInEndToEnd is the acceptance test for the acquisition path: a
// configured provider takes a person from Begin through the callback to a
// redeemed subject, and the person exists in the user model afterwards.
func TestOAuthSignInEndToEnd(t *testing.T) {
	provider := &fakeProvider{
		name: "fake-oauth",
		profile: goth.User{
			UserID: "114380",
			Email:  "person@example.com",
			Name:   "A Person",
		},
	}
	path := newTestGothic(t, provider)

	claim := beginAndCallback(t, path, "fake-oauth")

	subject, err := path.Authenticate(claim)
	require.NoError(t, err)
	require.Equal(t, "fake-oauth:114380", subject)

	record, err := path.users.GetBySubject(context.Background(), subject)
	require.NoError(t, err)
	require.Equal(t, "person@example.com", record.User.Email)
	require.Equal(t, "A Person", record.User.DisplayName)
}

// TestAClaimRedeemsOnce holds the replay property: the claim that crossed to
// the grant once is gone.
func TestAClaimRedeemsOnce(t *testing.T) {
	provider := &fakeProvider{
		name:    "fake-once",
		profile: goth.User{UserID: "one"},
	}
	path := newTestGothic(t, provider)

	claim := beginAndCallback(t, path, "fake-once")
	_, err := path.Authenticate(claim)
	require.NoError(t, err)

	_, err = path.Authenticate(claim)
	require.ErrorIs(t, err, ErrClaimInvalid)
}

// TestAnExpiredClaimIsRefused pins the TTL: a claim older than its window is
// as invalid as one that never existed.
func TestAnExpiredClaimIsRefused(t *testing.T) {
	provider := &fakeProvider{
		name:    "fake-expired",
		profile: goth.User{UserID: "late"},
	}
	path := newTestGothic(t, provider)
	issued := time.Now()
	path.now = func() time.Time { return issued }

	claim := beginAndCallback(t, path, "fake-expired")

	path.now = func() time.Time { return issued.Add(claimTTL + time.Second) }
	_, err := path.Authenticate(claim)
	require.ErrorIs(t, err, ErrClaimInvalid)
}

// TestAReturningSubjectIsTheSameUser holds the resolution contract: the
// second arrival of a subject refreshes the profile on the same user rather
// than minting another one.
func TestAReturningSubjectIsTheSameUser(t *testing.T) {
	provider := &fakeProvider{
		name:    "fake-return",
		profile: goth.User{UserID: "8181", Email: "old@example.com", Name: "Old Name"},
	}
	path := newTestGothic(t, provider)

	first := beginAndCallback(t, path, "fake-return")
	subject, err := path.Authenticate(first)
	require.NoError(t, err)
	before, err := path.users.GetBySubject(context.Background(), subject)
	require.NoError(t, err)

	provider.profile.Email = "new@example.com"
	provider.profile.Name = "New Name"
	second := beginAndCallback(t, path, "fake-return")
	_, err = path.Authenticate(second)
	require.NoError(t, err)

	after, err := path.users.GetBySubject(context.Background(), subject)
	require.NoError(t, err)
	require.Equal(t, before.User.ID, after.User.ID)
	require.Equal(t, "new@example.com", after.User.Email)
	require.Equal(t, "New Name", after.User.DisplayName)

	listed, err := path.users.List(context.Background(), 10, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
}

// TestAProviderNamingNoSubjectIsRefused covers the broken-provider case: a
// profile with no stable identifier must not become a user.
func TestAProviderNamingNoSubjectIsRefused(t *testing.T) {
	provider := &fakeProvider{
		name:    "fake-empty",
		profile: goth.User{UserID: "   "},
	}
	path := newTestGothic(t, provider)

	begin := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/identity/fake-empty", nil)
	require.NoError(t, path.Begin(begin, request, "fake-empty"))
	consent, err := url.Parse(begin.Header().Get("Location"))
	require.NoError(t, err)

	callback := httptest.NewRequest(http.MethodGet,
		CallbackPath("fake-empty")+"?state="+url.QueryEscape(consent.Query().Get("state")), nil)
	for _, cookie := range begin.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	_, err = path.Complete(httptest.NewRecorder(), callback, "fake-empty")
	require.Error(t, err)

	listed, err := path.users.List(context.Background(), 10, 0)
	require.NoError(t, err)
	require.Empty(t, listed)
}

// TestATamperedStateIsRefused pins the CSRF property gothic enforces: a
// callback whose state does not match the one Begin issued never reaches the
// user model.
func TestATamperedStateIsRefused(t *testing.T) {
	provider := &fakeProvider{
		name:    "fake-csrf",
		profile: goth.User{UserID: "csrf"},
	}
	path := newTestGothic(t, provider)

	begin := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/identity/fake-csrf", nil)
	require.NoError(t, path.Begin(begin, request, "fake-csrf"))

	callback := httptest.NewRequest(http.MethodGet,
		CallbackPath("fake-csrf")+"?state=forged", nil)
	for _, cookie := range begin.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	_, err := path.Complete(httptest.NewRecorder(), callback, "fake-csrf")
	require.Error(t, err)
}

// TestAnUnconfiguredProviderIsRefused holds the operator boundary: a
// provider goth knows about but this deployment did not configure is not
// served.
func TestAnUnconfiguredProviderIsRefused(t *testing.T) {
	provider := &fakeProvider{
		name:    "fake-served",
		profile: goth.User{UserID: "served"},
	}
	path := newTestGothic(t, provider)

	err := path.Begin(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/console/identity/other", nil), "other")
	require.ErrorIs(t, err, ErrUnknownOAuthProvider)

	_, err = path.Complete(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, CallbackPath("other"), nil), "other")
	require.ErrorIs(t, err, ErrUnknownOAuthProvider)
}

// TestNewGothicValidatesItsConfig pins each refusal an operator can draw
// from a bad OAuth config.
func TestNewGothicValidatesItsConfig(t *testing.T) {
	repositories := newTestRepositories(t)
	cases := []struct {
		name string
		cfg  OAuthConfig
		want error
	}{
		{"no providers", OAuthConfig{CallbackBaseURL: "http://localhost:8080"},
			ErrNoProvidersConfigured},
		{"no callback base", OAuthConfig{
			Providers: []OAuthProvider{{Name: "google", ClientID: "id", ClientSecret: "s"}}},
			ErrCallbackBaseRequired},
		{"unknown provider", OAuthConfig{
			CallbackBaseURL: "http://localhost:8080",
			Providers:       []OAuthProvider{{Name: "myspace", ClientID: "id", ClientSecret: "s"}}},
			ErrUnknownOAuthProvider},
		{"missing secret", OAuthConfig{
			CallbackBaseURL: "http://localhost:8080",
			Providers:       []OAuthProvider{{Name: "google", ClientID: "id"}}},
			ErrIncompleteOAuthProvider},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewGothic(testCase.cfg, repositories.Users)
			require.ErrorIs(t, err, testCase.want)
		})
	}
}

// TestNewGothicRegistersConfiguredProviders holds the happy construction
// path for the two shipped constructors.
func TestNewGothicRegistersConfiguredProviders(t *testing.T) {
	repositories := newTestRepositories(t)
	path, err := NewGothic(OAuthConfig{
		CallbackBaseURL: "http://localhost:8080/",
		Providers: []OAuthProvider{
			{Name: "google", ClientID: "gid", ClientSecret: "gsecret"},
			{Name: "github", ClientID: "hid", ClientSecret: "hsecret"},
		},
	}, repositories.Users)
	require.NoError(t, err)
	t.Cleanup(func() {
		delete(goth.GetProviders(), "google")
		delete(goth.GetProviders(), "github")
	})
	require.Equal(t, []string{"github", "google"}, path.Providers())

	registered, err := goth.GetProvider("google")
	require.NoError(t, err)
	require.Equal(t, "google", registered.Name())
}
