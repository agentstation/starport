package credentials

import (
	"context"
	"testing"
	"time"
)

func TestTerminalRefreshFailureInvalidatesRequestCache(t *testing.T) {
	for _, kind := range []SourceErrorKind{SourceErrorDenied, SourceErrorInvalid, SourceErrorNotConfigured, SourceErrorUnavailable} {
		t.Run(string(kind), func(t *testing.T) {
			fail := false
			source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
				if fail {
					return SourceMaterial{}, NewSourceError(kind, "test")
				}
				return NewSourceMaterial(map[string]string{"value": "valid-prior"}, "one", time.Time{}, nil), nil
			}}
			_, handle := testReferenceResolver(t, source)
			if _, err := handle.ResolveMaterial(t.Context()); err != nil {
				t.Fatal(err)
			}
			fail = true
			_, _, _ = handle.Refresh(t.Context())
			_, err := handle.CachedSource().ResolveMaterial(t.Context())
			if kind == SourceErrorUnavailable {
				if err != nil {
					t.Fatalf("temporary outage discarded usable material: %v", err)
				}
			} else if err == nil {
				t.Fatal("terminal refresh failure left material available to requests")
			}
		})
	}
}

func TestExpiredRefreshMaterialInvalidatesPriorRequestCache(t *testing.T) {
	expired := false
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		expiry := time.Time{}
		if expired {
			expiry = time.Now().Add(-time.Minute)
		}
		return NewSourceMaterial(map[string]string{"value": "valid-prior"}, "one", expiry, nil), nil
	}}
	_, handle := testReferenceResolver(t, source)
	if _, err := handle.ResolveMaterial(t.Context()); err != nil {
		t.Fatal(err)
	}
	expired = true
	if _, _, err := handle.Refresh(t.Context()); err == nil {
		t.Fatal("expired source material accepted")
	}
	if _, err := handle.CachedMaterial(t.Context()); err == nil {
		t.Fatal("expired replacement retained prior request material")
	}
}
