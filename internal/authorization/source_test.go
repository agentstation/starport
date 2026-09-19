package authorization

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
)

type revisionFunc func(context.Context) (revision.Stamp, error)

func (f revisionFunc) Read(ctx context.Context) (revision.Stamp, error) { return f(ctx) }

type keyFunc func(context.Context, string) (apikey.Record, error)

func (f keyFunc) GetByHash(ctx context.Context, id string) (apikey.Record, error) { return f(ctx, id) }

type accountFunc func(context.Context, string) (account.Record, error)

func (f accountFunc) GetByID(ctx context.Context, id string) (account.Record, error) {
	return f(ctx, id)
}

type teamFunc func(context.Context, string) (identity.TeamRecord, error)

func (f teamFunc) GetByID(ctx context.Context, id string) (identity.TeamRecord, error) {
	return f(ctx, id)
}

func sourceFixture(t *testing.T) (*RepositorySource, *[]string) {
	t.Helper()
	now := time.Unix(1000, 0)
	var reads []string
	candidate := cacheCandidate(Identity{Subject: "hash"}, now)
	candidate.Key.APIKey.TeamID = "team"
	stamp := func(name string) revisionFunc {
		return func(context.Context) (revision.Stamp, error) {
			reads = append(reads, name)
			return revision.Stamp{Epoch: name + "-epoch", Sequence: 1}, nil
		}
	}
	source, err := NewRepositorySource(RepositorySources{
		KV: stamp("kv"), SQL: stamp("sql"), KVAuthority: "kv", SQLAuthority: "sql",
		Keys: keyFunc(func(context.Context, string) (apikey.Record, error) {
			reads = append(reads, "key")
			return candidate.Key, nil
		}),
		Accounts: accountFunc(func(context.Context, string) (account.Record, error) {
			reads = append(reads, "account")
			return candidate.Account, nil
		}),
		Teams: teamFunc(func(context.Context, string) (identity.TeamRecord, error) {
			reads = append(reads, "team")
			return identity.TeamRecord{Revision: 1, Team: identity.Team{ID: "team", Name: "team"}}, nil
		}),
	}, authorityPair(t), func() (time.Time, bool) { return now, true }, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return source, &reads
}

func TestRepositorySourceCoherentInterval(t *testing.T) {
	s, reads := sourceFixture(t)
	result, err := s.Load(t.Context(), Identity{Subject: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"kv", "sql", "key", "account", "team", "sql", "kv"}; !reflect.DeepEqual(*reads, want) {
		t.Fatalf("reads = %v", *reads)
	}
	if result.Team == nil || len(result.Evidence) != 2 {
		t.Fatalf("incomplete candidate: %+v", result)
	}
	now, _ := s.clock()
	for _, e := range result.Evidence {
		if e.VerifiedAt != now || e.ValidUntil != now.Add(5*time.Minute) {
			t.Fatal("receipt changed original deadline")
		}
	}
}

func TestRepositorySourceObservesWithdrawalAfterRecordFailure(t *testing.T) {
	s, _ := sourceFixture(t)
	cache, err := NewCache(s, s.authorities, cacheTestLimits(), s.clock, testElapsedClock(s.clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	old, err := cache.Resolve(t.Context(), Identity{Subject: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	outage := errors.New("policy read unavailable")
	s.teams = teamFunc(func(context.Context, string) (identity.TeamRecord, error) { return identity.TeamRecord{}, outage })
	count := 0
	s.sql = revisionFunc(func(context.Context) (revision.Stamp, error) {
		count++
		return revision.Stamp{Epoch: "sql-epoch", Sequence: uint64(count)}, nil
	})
	if _, err := s.Load(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("changed candidate = %v", err)
	}
	now, _ := s.clock()
	if err := old.Permit().Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("retained permission = %v", err)
	}
}

func TestRepositorySourceDoesNotTreatFailureAsAbsence(t *testing.T) {
	for _, failure := range []error{identity.ErrTeamNotFound, ErrUnavailable} {
		t.Run(failure.Error(), func(t *testing.T) {
			s, _ := sourceFixture(t)
			s.teams = teamFunc(func(context.Context, string) (identity.TeamRecord, error) { return identity.TeamRecord{}, failure })
			if _, err := s.Load(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, failure) {
				t.Fatalf("required team = %v", err)
			}
		})
	}
	s, reads := sourceFixture(t)
	s.keys = keyFunc(func(context.Context, string) (apikey.Record, error) {
		return cacheCandidate(Identity{Subject: "hash"}, time.Unix(1000, 0)).Key, nil
	})
	candidate, err := s.Load(t.Context(), Identity{Subject: "hash"})
	if err != nil || candidate.Team != nil {
		t.Fatalf("teamless caller: %+v, %v", candidate, err)
	}
	for _, read := range *reads {
		if read == "team" {
			t.Fatal("teamless caller read team")
		}
	}
}

func TestRepositorySourceObservesHealthyAuthorityDuringOtherOutage(t *testing.T) {
	s, _ := sourceFixture(t)
	ticket, err := s.authorities.start()
	if err != nil {
		t.Fatal(err)
	}
	now, _ := s.clock()
	permit, err := ticket.accept([]Evidence{authorityEvidence("kv", "kv-epoch", 1, now), authorityEvidence("sql", "sql-epoch", 1, now)}, now, 5*time.Minute, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	s.kv = revisionFunc(func(context.Context) (revision.Stamp, error) { return revision.Stamp{}, ErrUnavailable })
	s.sql = revisionFunc(func(context.Context) (revision.Stamp, error) {
		return revision.Stamp{Epoch: "sql-epoch", Sequence: 2}, nil
	})
	if _, err := s.Load(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("outage = %v", err)
	}
	if err := permit.Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("retained permission = %v", err)
	}
}

func TestCacheRetriesWholeSourceAfterObservedChange(t *testing.T) {
	s, reads := sourceFixture(t)
	calls := 0
	s.kv = revisionFunc(func(context.Context) (revision.Stamp, error) {
		calls++
		seq := uint64(1)
		if calls > 1 {
			seq = 2
		}
		return revision.Stamp{Epoch: "kv-epoch", Sequence: seq}, nil
	})
	cache, err := NewCache(s, s.authorities, cacheTestLimits(), s.clock, testElapsedClock(s.clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	bundle, err := cache.Resolve(t.Context(), Identity{Subject: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("revision reads = %d", calls)
	}
	records := 0
	for _, read := range *reads {
		if read == "key" {
			records++
		}
	}
	if records != 2 {
		t.Fatalf("full key reads = %d", records)
	}
	if bundle.Permit().Evidence()[0].Sequence != 2 {
		t.Fatal("published stale sequence")
	}
}

func TestCacheBoundsSourceChangeRetries(t *testing.T) {
	s, _ := sourceFixture(t)
	calls := 0
	s.kv = revisionFunc(func(context.Context) (revision.Stamp, error) {
		calls++
		return revision.Stamp{Epoch: "kv-epoch", Sequence: uint64(calls)}, nil
	})
	cache, err := NewCache(s, s.authorities, cacheTestLimits(), s.clock, testElapsedClock(s.clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("unstable authority = %v", err)
	}
	if calls != 6 {
		t.Fatalf("revision reads = %d, want six for three attempts", calls)
	}
}

func TestRepositorySourceRejectsForeignDependencies(t *testing.T) {
	for _, dependency := range []string{"key", "account", "team"} {
		t.Run(dependency, func(t *testing.T) {
			s, _ := sourceFixture(t)
			switch dependency {
			case "key":
				s.keys = keyFunc(func(context.Context, string) (apikey.Record, error) {
					return apikey.Record{Revision: 1, APIKey: apikey.APIKey{ID: "other", Hash: "foreign"}}, nil
				})
			case "account":
				s.accounts = accountFunc(func(context.Context, string) (account.Record, error) {
					return account.Record{Revision: 1, Account: account.Account{ID: "foreign"}}, nil
				})
			case "team":
				s.teams = teamFunc(func(context.Context, string) (identity.TeamRecord, error) {
					return identity.TeamRecord{Revision: 1, Team: identity.Team{ID: "foreign"}}, nil
				})
			}
			if _, err := s.Load(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrEvidence) {
				t.Fatalf("foreign dependency = %v", err)
			}
		})
	}
}

func TestRepositorySourceNeverExtendsValidityForSlowReads(t *testing.T) {
	s, _ := sourceFixture(t)
	started := time.Unix(1000, 0)
	now := started
	s.clock = func() (time.Time, bool) { return now, true }
	original := s.keys
	s.keys = keyFunc(func(ctx context.Context, id string) (apikey.Record, error) {
		now = now.Add(time.Minute)
		return original.GetByHash(ctx, id)
	})
	candidate, err := s.Load(t.Context(), Identity{Subject: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range candidate.Evidence {
		if e.VerifiedAt != started || e.ValidUntil != started.Add(5*time.Minute) {
			t.Fatal("slow read extended permission")
		}
	}
}

func TestRepositorySourceRejectsClockFailure(t *testing.T) {
	for _, mode := range []string{"unknown", "backward"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := sourceFixture(t)
			calls := 0
			s.clock = func() (time.Time, bool) {
				calls++
				now := time.Unix(1000, 0)
				if calls == 1 {
					return now, true
				}
				return now.Add(-time.Second), mode != "unknown"
			}
			if _, err := s.Load(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("clock failure = %v", err)
			}
		})
	}
}
