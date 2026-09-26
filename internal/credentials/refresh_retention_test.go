package credentials

import (
	"context"
	"errors"
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
			issued, err := handle.ResolveMaterial(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			fail = true
			_, _, _ = handle.Refresh(t.Context())
			_, err = handle.CachedSource().ResolveMaterial(t.Context())
			if kind == SourceErrorUnavailable {
				if err != nil {
					t.Fatalf("temporary outage discarded usable material: %v", err)
				}
				if err := issued.CheckValidity(time.Now()); err != nil {
					t.Fatalf("temporary outage revoked issued material: %v", err)
				}
			} else if err == nil {
				t.Fatal("terminal refresh failure left material available to requests")
			} else if err := issued.CheckValidity(time.Now()); !errors.Is(err, ErrMaterialRevoked) {
				t.Fatalf("terminal refresh left issued material valid: %v", err)
			}
		})
	}
}

func TestResolverRevokesIssuedMaterialAndRecovers(t *testing.T) {
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		return NewSourceMaterial(map[string]string{"value": "fixture-secret"}, "one", time.Time{}, nil), nil
	}}
	_, handle := testReferenceResolver(t, source)
	issued, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Revoke(); err != nil {
		t.Fatal(err)
	}
	if err := issued.CheckValidity(time.Now()); !errors.Is(err, ErrMaterialRevoked) {
		t.Fatalf("issued material survived revocation: %v", err)
	}
	recovered, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.CheckValidity(time.Now()); err != nil {
		t.Fatalf("recovered material is invalid: %v", err)
	}
	if err := issued.CheckValidity(time.Now()); !errors.Is(err, ErrMaterialRevoked) {
		t.Fatalf("recovery restored revoked material: %v", err)
	}
}

func TestResolverRefreshKeepsIssuedDeadlineAndFencesRotation(t *testing.T) {
	now := time.Now()
	value, version, expiry := "fixture-first", "one", now.Add(time.Minute)
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		return NewSourceMaterial(map[string]string{"value": value}, version, expiry, nil), nil
	}}
	_, handle := testReferenceResolver(t, source)
	issued, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expiry = now.Add(2 * time.Minute)
	renewed, _, err := handle.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := issued.CheckValidity(now); err != nil {
		t.Fatalf("unchanged refresh revoked valid material: %v", err)
	}
	if err := issued.CheckValidity(now.Add(time.Minute)); !errors.Is(err, ErrMaterialExpired) {
		t.Fatalf("refresh extended an issued deadline: %v", err)
	}
	if err := renewed.CheckValidity(now.Add(time.Minute)); err != nil {
		t.Fatalf("new deadline was not published: %v", err)
	}
	value, version = "fixture-second", "two"
	rotated, _, err := handle.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, material := range []Material{issued, renewed} {
		if err := material.CheckValidity(now); !errors.Is(err, ErrMaterialRevoked) {
			t.Fatalf("rotation left an old handle valid: %v", err)
		}
	}
	if err := rotated.CheckValidity(now); err != nil {
		t.Fatalf("rotated material is invalid: %v", err)
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
