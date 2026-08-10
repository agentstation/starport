package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeTrackingTransport struct {
	closed bool
}

type closeTrackingBody struct {
	closed bool
}

func (*closeTrackingBody) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func (t *closeTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected request")
}

func (t *closeTrackingTransport) CloseIdleConnections() {
	t.closed = true
}

func TestBearerTransportAuthorizesARequestCopy(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://provider.test/inference", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "original")
	base := roundTripperFunc(func(got *http.Request) (*http.Response, error) {
		if value := got.Header.Get("Authorization"); value != "Bearer renewable" {
			t.Errorf("Authorization = %q", value)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
			Request:    got,
		}, nil
	})
	transport := NewBearerTransport(base, SourceFunc(func(context.Context) (Token, error) {
		return Token{Value: "renewable", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}))

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if value := request.Header.Get("Authorization"); value != "original" {
		t.Errorf("original Authorization = %q", value)
	}
}

func TestBearerTransportAddsGoogleQuotaProject(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://provider.test/inference", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	base := roundTripperFunc(func(got *http.Request) (*http.Response, error) {
		if value := got.Header.Get(googleQuotaProjectHeader); value != "quota-project" {
			t.Errorf("%s = %q", googleQuotaProjectHeader, value)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
			Request:    got,
		}, nil
	})
	transport := NewBearerTransport(base, SourceFunc(func(context.Context) (Token, error) {
		return Token{
			Value:          "renewable",
			ExpiresAt:      time.Now().Add(time.Hour),
			QuotaProjectID: "quota-project",
		}, nil
	}))

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if value := request.Header.Get(googleQuotaProjectHeader); value != "" {
		t.Errorf("original %s = %q", googleQuotaProjectHeader, value)
	}
}

func TestBearerTransportPreservesExplicitGoogleQuotaProject(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://provider.test/inference", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set(googleQuotaProjectHeader, "request-project")
	base := roundTripperFunc(func(got *http.Request) (*http.Response, error) {
		if value := got.Header.Get(googleQuotaProjectHeader); value != "request-project" {
			t.Errorf("%s = %q", googleQuotaProjectHeader, value)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
			Request:    got,
		}, nil
	})
	transport := NewBearerTransport(base, SourceFunc(func(context.Context) (Token, error) {
		return Token{
			Value:          "renewable",
			ExpiresAt:      time.Now().Add(time.Hour),
			QuotaProjectID: "credential-project",
		}, nil
	}))

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

func TestBearerTransportRejectsCredentialRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if value := request.Header.Get("Authorization"); value != "Bearer renewable" {
			t.Errorf("Authorization = %q", value)
		}
		if value := request.Header.Get(googleQuotaProjectHeader); value != "quota-project" {
			t.Errorf("%s = %q", googleQuotaProjectHeader, value)
		}
		http.Redirect(w, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	client := &http.Client{
		Transport: NewBearerTransport(http.DefaultTransport, SourceFunc(
			func(context.Context) (Token, error) {
				return Token{
					Value:          "renewable",
					ExpiresAt:      time.Now().Add(time.Hour),
					QuotaProjectID: "quota-project",
				}, nil
			},
		)),
	}
	response, err := client.Get(origin.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, ErrCredentialRedirect) {
		t.Fatalf("redirect error = %v, want credential redirect rejection", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("redirect target requests = %d, want 0", got)
	}
}

func TestBearerTransportForwardsRequestCancellation(t *testing.T) {
	called := false
	source := SourceFunc(func(ctx context.Context) (Token, error) {
		called = true
		<-ctx.Done()
		return Token{}, ctx.Err()
	})
	transport := NewBearerTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("base transport received a request after credential failure")
		return nil, nil
	}), source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := &closeTrackingBody{}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.test/inference", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	_, err = transport.RoundTrip(request)
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("round trip error = %v; source called = %t", err, called)
	}
	if !body.closed {
		t.Fatal("request body remained open after credential failure")
	}
}

func TestBearerTransportClosesBodyWhenSourceIsMissing(t *testing.T) {
	body := &closeTrackingBody{}
	request, err := http.NewRequest(http.MethodPost, "https://provider.test/inference", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	transport := NewBearerTransport(nil, nil)
	_, err = transport.RoundTrip(request)
	if !errors.Is(err, ErrSourceRequired) {
		t.Fatalf("round trip error = %v, want missing source", err)
	}
	if !body.closed {
		t.Fatal("request body remained open without a credential source")
	}
}

func TestBearerTransportClosesWrappedIdleConnections(t *testing.T) {
	base := &closeTrackingTransport{}
	transport := NewBearerTransport(base, SourceFunc(func(context.Context) (Token, error) {
		return Token{Value: "token"}, nil
	}))
	closer, ok := transport.(interface{ CloseIdleConnections() })
	if !ok {
		t.Fatal("bearer transport does not expose idle connection closure")
	}
	closer.CloseIdleConnections()
	if !base.closed {
		t.Fatal("wrapped idle connections were not closed")
	}
}
