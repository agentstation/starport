package credentials

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func TestExplicitCredentialReferencesPrecedeAmbientSources(t *testing.T) {
	provider := staticCredentialProvider()

	t.Run("explicit environment reference precedes ambient discovery", func(t *testing.T) {
		lookups := make([]string, 0, 2)
		lookup := func(name string) (string, bool) {
			lookups = append(lookups, name)
			values := map[string]string{
				"EXPLICIT_OPENAI_KEY": "valid-explicit",
				"OPENAI_API_KEY":      "valid-ambient",
			}
			value, found := values[name]
			return value, found
		}
		handle := referenceHandle(t, provider, NewResolver(WithEnvironmentLookup(lookup)),
			"env:EXPLICIT_OPENAI_KEY", false)
		material, err := handle.ResolveMaterial(t.Context())
		if err != nil {
			t.Fatalf("resolve material: %v", err)
		}
		if value, _ := material.Value("api-key"); value != "valid-explicit" {
			t.Fatalf("api-key = %q", value)
		}
		if len(lookups) != 1 || lookups[0] != "EXPLICIT_OPENAI_KEY" {
			t.Fatalf("lookups = %#v", lookups)
		}
	})

	t.Run("missing explicit reference fails closed", func(t *testing.T) {
		lookups := make([]string, 0, 2)
		lookup := func(name string) (string, bool) {
			lookups = append(lookups, name)
			if name == "OPENAI_API_KEY" {
				return "valid-ambient", true
			}
			return "", false
		}
		handle := referenceHandle(t, provider, NewResolver(WithEnvironmentLookup(lookup)),
			"env:MISSING_OPENAI_KEY", false)
		_, err := handle.ResolveMaterial(t.Context())
		if !IsSourceError(err, SourceErrorNotConfigured) {
			t.Fatalf("resolve error = %v", err)
		}
		if strings.Contains(err.Error(), "MISSING_OPENAI_KEY") ||
			strings.Contains(err.Error(), "valid-ambient") {
			t.Fatalf("error exposed source details: %v", err)
		}
		if len(lookups) != 1 || lookups[0] != "MISSING_OPENAI_KEY" {
			t.Fatalf("lookups = %#v", lookups)
		}
	})

	t.Run("typed not-configured fallback permits ambient discovery", func(t *testing.T) {
		lookup := mapLookup(map[string]string{"OPENAI_API_KEY": "valid-ambient"})
		handle := referenceHandle(t, provider, NewResolver(WithEnvironmentLookup(lookup)),
			"env:MISSING_OPENAI_KEY", true)
		material, err := handle.ResolveMaterial(t.Context())
		if err != nil {
			t.Fatalf("resolve material: %v", err)
		}
		if value, _ := material.Value("api-key"); value != "valid-ambient" {
			t.Fatalf("api-key = %q", value)
		}
	})

	t.Run("file reference preserves exact bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "provider.key")
		const value = "valid-file-key\n"
		writeCredentialFile(t, path, value)
		provider := staticCredentialProvider()
		provider.Credentials.Fields[0].Pattern = ""
		handle := referenceHandle(t, provider, NewResolver(), "file:"+path, false)
		material, err := handle.ResolveMaterial(t.Context())
		if err != nil {
			t.Fatalf("resolve material: %v", err)
		}
		if got, _ := material.Value("api-key"); got != value {
			t.Fatalf("api-key bytes = %q, want %q", got, value)
		}
	})
}

func TestCredentialSourceConformance(t *testing.T) {
	vectors := []struct {
		id  string
		run func(*testing.T)
	}{
		{id: "static", run: conformanceStatic},
		{id: "default_chain", run: conformanceDefaultChain},
		{id: "version", run: conformanceVersion},
		{id: "expiry", run: conformanceExpiry},
		{id: "lease", run: conformanceLease},
		{id: "cancellation", run: conformanceCancellation},
		{id: "concurrency", run: conformanceConcurrency},
		{id: "denial", run: conformanceDenial},
		{id: "redaction", run: conformanceRedaction},
		{id: "rotation_in_place", run: conformanceRotationInPlace},
		{id: "rotation_atomic_replace", run: conformanceRotationAtomicReplace},
		{id: "rotation_symlink_swap", run: conformanceRotationSymlinkSwap},
		{id: "rotation_mounted_replace", run: conformanceRotationMountedReplace},
		{id: "rotation_agent_rerender", run: conformanceRotationAgentRerender},
	}
	for _, vector := range vectors {
		t.Run(vector.id, vector.run)
	}
}

func TestCredentialResolverWarmCacheHitLatencyAndConcurrency(t *testing.T) {
	var backendCalls atomic.Int32
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		backendCalls.Add(1)
		return NewSourceMaterial(
			map[string]string{"value": "valid-cached"},
			"cached-version",
			time.Time{},
			nil,
		), nil
	}}
	provider, handle := testReferenceResolver(t, source)
	_ = provider

	if _, err := handle.ResolveMaterial(t.Context()); err != nil {
		t.Fatalf("warm material: %v", err)
	}
	if got := backendCalls.Load(); got != 1 {
		t.Fatalf("warm backend calls = %d", got)
	}

	const samples = 10_000
	durations := make([]time.Duration, 0, samples)
	for range samples {
		started := time.Now()
		if _, err := handle.ResolveMaterial(t.Context()); err != nil {
			t.Fatalf("cached material: %v", err)
		}
		durations = append(durations, time.Since(started))
	}
	if got := backendCalls.Load(); got != 1 {
		t.Fatalf("backend calls after cache hits = %d", got)
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	p95 := durations[(samples*95)/100-1]
	t.Logf("warm cache-hit samples = %d, p95 = %s", samples, p95)
	if p95 > time.Millisecond {
		t.Fatalf("warm cache-hit p95 = %s, limit = 1ms", p95)
	}

	const workers = 64
	const hitsPerWorker = 100
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range hitsPerWorker {
				if _, err := handle.ResolveMaterial(context.Background()); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent cache hit: %v", err)
	}
	if got := backendCalls.Load(); got != 1 {
		t.Fatalf("backend calls after concurrent hits = %d", got)
	}
}

func TestCredentialLifecycleRefreshFailureRevocationAndLeaderCancellation(t *testing.T) {
	t.Run("failed refresh retains prior material", func(t *testing.T) {
		calls := 0
		source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
			calls++
			if calls == 1 {
				return NewSourceMaterial(
					map[string]string{"value": "valid-prior"}, "prior", time.Time{}, nil,
				), nil
			}
			return SourceMaterial{}, NewSourceError(SourceErrorUnavailable, "test")
		}}
		_, handle := testReferenceResolver(t, source)
		prior, err := handle.ResolveMaterial(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := handle.Refresh(t.Context()); !IsSourceError(err, SourceErrorUnavailable) {
			t.Fatalf("refresh error = %v", err)
		}
		retained, err := handle.ResolveMaterial(t.Context())
		if err != nil || retained.Version() != prior.Version() || calls != 2 {
			t.Fatalf("retained material = %q after %d calls, %v", retained.Version(), calls, err)
		}
	})

	t.Run("revocation prevents in-flight cache publication", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
			close(entered)
			<-release
			return NewSourceMaterial(
				map[string]string{"value": "valid-late"}, "late", time.Time{}, nil,
			), nil
		}}
		_, handle := testReferenceResolver(t, source)
		result := make(chan error, 1)
		go func() {
			_, err := handle.ResolveMaterial(context.Background())
			result <- err
		}()
		<-entered
		if err := handle.Revoke(); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := <-result; !errors.Is(err, ErrMaterialRevoked) {
			t.Fatalf("in-flight result = %v", err)
		}
	})

	t.Run("waiter retries after leader cancellation", func(t *testing.T) {
		var calls atomic.Int32
		entered := make(chan struct{})
		source := &testReferenceSource{backend: "test", resolve: func(ctx context.Context, _ Reference) (SourceMaterial, error) {
			if calls.Add(1) == 1 {
				close(entered)
				<-ctx.Done()
				return SourceMaterial{}, ctx.Err()
			}
			return NewSourceMaterial(
				map[string]string{"value": "valid-retry"}, "retry", time.Time{}, nil,
			), nil
		}}
		_, handle := testReferenceResolver(t, source)
		leaderContext, cancelLeader := context.WithCancel(t.Context())
		leader := make(chan error, 1)
		go func() {
			_, err := handle.ResolveMaterial(leaderContext)
			leader <- err
		}()
		<-entered
		waiter := make(chan error, 1)
		go func() {
			material, err := handle.ResolveMaterial(context.Background())
			if err == nil {
				value, _ := material.Value("api-key")
				if value != "valid-retry" {
					err = errors.New("waiter received unexpected material")
				}
			}
			waiter <- err
		}()
		cancelLeader()
		if err := <-leader; !errors.Is(err, context.Canceled) {
			t.Fatalf("leader error = %v", err)
		}
		if err := <-waiter; err != nil {
			t.Fatalf("waiter error = %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("source calls = %d", got)
		}
	})
}

func TestCredentialResolverRefreshesExpiredAndLeasedMaterial(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata func(time.Time) (time.Time, *Lease)
		advance  time.Duration
	}{
		{
			name: "expiry",
			metadata: func(now time.Time) (time.Time, *Lease) {
				return now.Add(time.Hour), nil
			},
			advance: time.Hour,
		},
		{
			name: "lease refresh time",
			metadata: func(now time.Time) (time.Time, *Lease) {
				return now.Add(time.Hour), &Lease{
					Renewable: true, RefreshAfter: now.Add(time.Minute),
				}
			},
			advance: time.Minute,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1_000, 0)
			calls := 0
			source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
				calls++
				expiresAt, lease := test.metadata(now)
				return NewSourceMaterial(
					map[string]string{"value": "valid-token"},
					"source-version",
					expiresAt,
					lease,
				), nil
			}}
			provider := staticCredentialProvider()
			provider.Credentials.Fields[0].Pattern = ""
			resolver := NewResolver(
				WithResolverClock(func() time.Time { return now }),
				WithReferenceSource(source),
			)
			handle := referenceHandle(t, provider, resolver, "test:resource", false)
			if _, err := handle.ResolveMaterial(t.Context()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(test.advance)
			if _, err := handle.ResolveMaterial(t.Context()); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("source calls = %d, want 2", calls)
			}
		})
	}
}

func TestFileSourceEnforcesPathAndSizePolicy(t *testing.T) {
	provider := staticCredentialProvider()
	provider.Credentials.Fields[0].Pattern = ""

	t.Run("absolute path", func(t *testing.T) {
		handle := referenceHandle(t, provider, NewResolver(), "file:relative.key", false)
		_, err := handle.ResolveMaterial(t.Context())
		if !IsSourceError(err, SourceErrorInvalid) {
			t.Fatalf("relative path error = %v", err)
		}
	})

	t.Run("regular file", func(t *testing.T) {
		handle := referenceHandle(t, provider, NewResolver(), "file:"+t.TempDir(), false)
		_, err := handle.ResolveMaterial(t.Context())
		if !IsSourceError(err, SourceErrorInvalid) {
			t.Fatalf("directory error = %v", err)
		}
	})

	t.Run("size limit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "oversize.key")
		writeCredentialFile(t, path, strings.Repeat("x", maxCredentialFileBytes+1))
		handle := referenceHandle(t, provider, NewResolver(), "file:"+path, false)
		_, err := handle.ResolveMaterial(t.Context())
		if !IsSourceError(err, SourceErrorInvalid) {
			t.Fatalf("oversize error = %v", err)
		}
	})
}

func TestParseReferenceGrammar(t *testing.T) {
	for _, value := range []string{
		"env:OPENAI_API_KEY",
		"file:/run/secrets/key",
		"vault:secret/data/app?version=2#api-key",
	} {
		if _, err := ParseReference(value); err != nil {
			t.Errorf("ParseReference(%q): %v", value, err)
		}
	}
	for _, value := range []string{
		"", "ENV:name", "env:", "env:NAME?", "env:NAME#",
		"env:NAME?other=x", "env:NAME?version=", "env:NAME?version=1&version=2",
		"env:NAME#field#more",
	} {
		if _, err := ParseReference(value); err == nil {
			t.Errorf("ParseReference accepted %q", value)
		}
	}
}

func conformanceStatic(t *testing.T) {
	provider := staticCredentialProvider()
	resolver := NewResolver(WithEnvironmentLookup(mapLookup(map[string]string{
		"OPENAI_API_KEY": "valid-static",
	})))
	handle, err := resolver.Provider(provider, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	material, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := material.Value("api-key"); value != "valid-static" {
		t.Fatalf("api-key = %q", value)
	}
	if material.Version() == "" {
		t.Fatal("static material has no opaque version")
	}
	if _, found := material.ExpiresAt(); found {
		t.Fatal("static material has an expiry")
	}
}

func conformanceDefaultChain(t *testing.T) {
	provider := defaultChainCredentialProvider()
	resolver := NewResolver(WithCloudChain(
		catalogs.ProviderAuthenticationGoogleDefault,
		CloudChainFunc(func(
			context.Context,
			catalogs.ProviderCredentialProfile,
			map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
		) (SourceMaterial, error) {
			return NewSourceMaterial(
				map[string]string{"access-token": "token"}, "chain", time.Time{}, nil,
			), nil
		}),
	))
	handle, err := resolver.Provider(provider, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	material, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := material.Value("access-token"); value != "token" {
		t.Fatalf("access-token = %q", value)
	}
}

func conformanceVersion(t *testing.T) {
	values := map[string]string{"OPENAI_API_KEY": "valid-one"}
	resolver := NewResolver(WithEnvironmentLookup(mapLookup(values)))
	handle, err := resolver.Provider(staticCredentialProvider(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	values["OPENAI_API_KEY"] = "valid-two"
	second, configured, err := handle.Refresh(t.Context())
	if err != nil || !configured {
		t.Fatalf("refresh = %t, %v", configured, err)
	}
	if first.Version() == "" || first.Version() == second.Version() {
		t.Fatalf("versions = %q and %q", first.Version(), second.Version())
	}
}

func conformanceExpiry(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	material := resolveTestSource(t, NewSourceMaterial(
		map[string]string{"value": "valid-expiring"}, "expiring", expiresAt, nil,
	))
	got, found := material.ExpiresAt()
	if !found || !got.Equal(expiresAt) {
		t.Fatalf("expiry = %v, %t", got, found)
	}
}

func conformanceLease(t *testing.T) {
	refreshAfter := time.Now().UTC().Add(time.Minute)
	material := resolveTestSource(t, NewSourceMaterial(
		map[string]string{"value": "valid-leased"},
		"leased",
		time.Time{},
		&Lease{Renewable: true, RefreshAfter: refreshAfter},
	))
	lease, found := material.Lease()
	if !found || !lease.Renewable || !lease.RefreshAfter.Equal(refreshAfter) {
		t.Fatalf("lease = %#v, %t", lease, found)
	}
}

func conformanceCancellation(t *testing.T) {
	source := &testReferenceSource{backend: "test", resolve: func(ctx context.Context, _ Reference) (SourceMaterial, error) {
		<-ctx.Done()
		return SourceMaterial{}, ctx.Err()
	}}
	_, handle := testReferenceResolver(t, source)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	_, err := handle.ResolveMaterial(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("resolve error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func conformanceConcurrency(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return NewSourceMaterial(
			map[string]string{"value": "valid-concurrent"}, "one", time.Time{}, nil,
		), nil
	}}
	_, handle := testReferenceResolver(t, source)

	const workers = 32
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := handle.ResolveMaterial(context.Background())
			errs <- err
		}()
	}
	<-entered
	close(release)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("resolve material: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("source calls = %d", got)
	}
}

func conformanceDenial(t *testing.T) {
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		return SourceMaterial{}, NewSourceError(SourceErrorDenied, "test")
	}}
	_, handle := testReferenceResolver(t, source)
	_, err := handle.ResolveMaterial(t.Context())
	if !IsSourceError(err, SourceErrorDenied) {
		t.Fatalf("resolve error = %v", err)
	}
}

func conformanceRedaction(t *testing.T) {
	const sensitivePath = "/private/operator/customer-a/openai-secret"
	handle := referenceHandle(
		t,
		staticCredentialProvider(),
		NewResolver(),
		"file:"+sensitivePath,
		false,
	)
	_, err := handle.ResolveMaterial(t.Context())
	if err == nil {
		t.Fatal("resolve succeeded")
	}
	for _, forbidden := range []string{sensitivePath, "customer-a", "openai-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposed %q: %v", forbidden, err)
		}
	}
}

func conformanceRotationInPlace(t *testing.T) {
	path, handle := fileRotationFixture(t, "valid-before")
	first := resolveVersion(t, handle)
	writeCredentialFile(t, path, "valid-after")
	second := refreshVersion(t, handle)
	assertVersionChanged(t, first, second)
}

func conformanceRotationAtomicReplace(t *testing.T) {
	path, handle := fileRotationFixture(t, "valid-before")
	first := resolveVersion(t, handle)
	replacement := path + ".next"
	writeCredentialFile(t, replacement, "valid-after")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	second := refreshVersion(t, handle)
	assertVersionChanged(t, first, second)
}

func conformanceRotationSymlinkSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics require elevated Windows privileges")
	}
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "secret.1")
	secondPath := filepath.Join(dir, "secret.2")
	writeCredentialFile(t, firstPath, "valid-before")
	writeCredentialFile(t, secondPath, "valid-after")
	link := filepath.Join(dir, "secret")
	if err := os.Symlink(firstPath, link); err != nil {
		t.Fatal(err)
	}
	handle := resolverForFile(t, link)
	first := resolveVersion(t, handle)
	nextLink := link + ".next"
	if err := os.Symlink(secondPath, nextLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(nextLink, link); err != nil {
		t.Fatal(err)
	}
	second := refreshVersion(t, handle)
	assertVersionChanged(t, first, second)
}

func conformanceRotationMountedReplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("projected-volume symlink semantics require elevated Windows privileges")
	}
	dir := t.TempDir()
	firstData := filepath.Join(dir, "..2026_08_10_1")
	secondData := filepath.Join(dir, "..2026_08_10_2")
	if err := os.Mkdir(firstData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secondData, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCredentialFile(t, filepath.Join(firstData, "api-key"), "valid-before")
	writeCredentialFile(t, filepath.Join(secondData, "api-key"), "valid-after")
	dataLink := filepath.Join(dir, "..data")
	if err := os.Symlink(firstData, dataLink); err != nil {
		t.Fatal(err)
	}
	secretLink := filepath.Join(dir, "api-key")
	if err := os.Symlink(filepath.Join("..data", "api-key"), secretLink); err != nil {
		t.Fatal(err)
	}
	handle := resolverForFile(t, secretLink)
	first := resolveVersion(t, handle)
	nextDataLink := filepath.Join(dir, "..data.next")
	if err := os.Symlink(secondData, nextDataLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(nextDataLink, dataLink); err != nil {
		t.Fatal(err)
	}
	second := refreshVersion(t, handle)
	assertVersionChanged(t, first, second)
}

func conformanceRotationAgentRerender(t *testing.T) {
	path, handle := fileRotationFixture(t, "valid-before")
	first := resolveVersion(t, handle)
	templateOutput := filepath.Join(filepath.Dir(path), ".agent-render")
	writeCredentialFile(t, templateOutput, "valid-after")
	if err := os.Rename(templateOutput, path); err != nil {
		t.Fatal(err)
	}
	second := refreshVersion(t, handle)
	assertVersionChanged(t, first, second)
}

type testReferenceSource struct {
	backend ReferenceBackend
	resolve func(context.Context, Reference) (SourceMaterial, error)
}

func (s *testReferenceSource) Backend() ReferenceBackend { return s.backend }

func (s *testReferenceSource) Resolve(
	ctx context.Context,
	reference Reference,
) (SourceMaterial, error) {
	return s.resolve(ctx, reference)
}

func resolveTestSource(t *testing.T, material SourceMaterial) Material {
	t.Helper()
	source := &testReferenceSource{backend: "test", resolve: func(context.Context, Reference) (SourceMaterial, error) {
		return material, nil
	}}
	_, handle := testReferenceResolver(t, source)
	resolved, err := handle.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func testReferenceResolver(
	t *testing.T,
	source ReferenceSource,
) (catalogs.Provider, *ProviderHandle) {
	t.Helper()
	provider := staticCredentialProvider()
	provider.Credentials.Fields[0].Pattern = ""
	resolver := NewResolver(WithReferenceSource(source))
	handle := referenceHandle(t, provider, resolver, string(source.Backend())+":resource", false)
	return provider, handle
}

func referenceHandle(
	t testing.TB,
	provider catalogs.Provider,
	resolver *Resolver,
	value string,
	fallback bool,
) *ProviderHandle {
	t.Helper()
	reference, err := ParseReference(value)
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	handle, err := resolver.Provider(provider, map[catalogs.ProviderCredentialFieldID]ReferencePolicy{
		"api-key": {Reference: reference, FallbackAmbient: fallback},
	}, false)
	if err != nil {
		t.Fatalf("provider handle: %v", err)
	}
	return handle
}

func fileRotationFixture(t *testing.T, value string) (string, *ProviderHandle) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-key")
	writeCredentialFile(t, path, value)
	return path, resolverForFile(t, path)
}

func resolverForFile(t testing.TB, path string) *ProviderHandle {
	t.Helper()
	provider := staticCredentialProvider()
	provider.Credentials.Fields[0].Pattern = ""
	return referenceHandle(t, provider, NewResolver(), "file:"+path, false)
}

func writeCredentialFile(t testing.TB, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	fixed := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, fixed, fixed); err != nil {
		t.Fatalf("set credential file time: %v", err)
	}
}

func resolveVersion(t testing.TB, handle *ProviderHandle) string {
	t.Helper()
	material, err := handle.ResolveMaterial(context.Background())
	if err != nil {
		t.Fatalf("resolve material: %v", err)
	}
	if material.Version() == "" {
		t.Fatal("credential version is empty")
	}
	return material.Version()
}

func refreshVersion(t testing.TB, handle *ProviderHandle) string {
	t.Helper()
	material, configured, err := handle.Refresh(context.Background())
	if err != nil || !configured {
		t.Fatalf("refresh material = %t, %v", configured, err)
	}
	if material.Version() == "" {
		t.Fatal("credential version is empty")
	}
	return material.Version()
}

func assertVersionChanged(t testing.TB, first, second string) {
	t.Helper()
	if first == second {
		t.Fatalf("credential version did not change: %q", first)
	}
}

func staticCredentialProvider() catalogs.Provider {
	return catalogs.Provider{
		ID: "openai", Name: "OpenAI",
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
				Environment: []string{"OPENAI_API_KEY"}, Pattern: `^valid-`,
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
				Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"},
			},
		},
	}
}

func defaultChainCredentialProvider() catalogs.Provider {
	return catalogs.Provider{
		ID: "cloud", Name: "Cloud",
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID: "access-token", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "default", Primitive: catalogs.ProviderAuthenticationGoogleDefault,
				Fields: []catalogs.ProviderCredentialFieldID{"access-token"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "access-token", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
				Scopes: []string{"https://example.test/.default"},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"default"},
			},
		},
	}
}

func mapLookup(values map[string]string) EnvironmentLookup {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}
