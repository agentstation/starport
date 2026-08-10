package providerauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshingSourceRefreshesBeforeExpiry(t *testing.T) {
	now := time.Unix(1_000, 0)
	var calls int
	upstream := SourceFunc(func(context.Context) (Token, error) {
		calls++
		return Token{
			Value:     fmt.Sprintf("token-%d", calls),
			ExpiresAt: now.Add(10 * time.Minute),
		}, nil
	})
	source, err := NewRefreshingSource(upstream, RefreshOptions{
		RefreshBefore: 2 * time.Minute,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	first, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	now = now.Add(7 * time.Minute)
	cached, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("cached token: %v", err)
	}
	if cached.Value != first.Value || calls != 1 {
		t.Fatalf("cached token = %q after %d calls, want %q after 1", cached.Value, calls, first.Value)
	}

	now = now.Add(time.Minute)
	refreshed, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("refreshed token: %v", err)
	}
	if refreshed.Value == first.Value || calls != 2 {
		t.Fatalf("refreshed token = %q after %d calls", refreshed.Value, calls)
	}
}

func TestRefreshingSourceCoalescesConcurrentRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := SourceFunc(func(context.Context) (Token, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return Token{Value: "shared", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	source, err := NewRefreshingSource(upstream, RefreshOptions{})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	const callers = 24
	results := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	begin := make(chan struct{})
	for range callers {
		go func() {
			ready.Done()
			<-begin
			token, tokenErr := source.Token(context.Background())
			if tokenErr == nil && token.Value != "shared" {
				tokenErr = errors.New("unexpected token")
			}
			results <- tokenErr
		}()
	}
	ready.Wait()
	close(begin)
	<-started
	close(release)
	for range callers {
		if result := <-results; result != nil {
			t.Errorf("token result: %v", result)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
}

func TestRefreshingSourceWaiterRespectsCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := SourceFunc(func(context.Context) (Token, error) {
		close(started)
		<-release
		return Token{Value: "shared", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	source, err := NewRefreshingSource(upstream, RefreshOptions{})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	primary := make(chan error, 1)
	go func() {
		_, tokenErr := source.Token(context.Background())
		primary <- tokenErr
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = source.Token(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting error = %v, want context cancellation", err)
	}
	close(release)
	if err := <-primary; err != nil {
		t.Fatalf("primary refresh: %v", err)
	}
}

func TestRefreshingSourceRejectsInvalidTokens(t *testing.T) {
	now := time.Unix(1_000, 0)
	tests := []struct {
		name  string
		token Token
		want  error
	}{
		{name: "empty", token: Token{}, want: ErrTokenEmpty},
		{name: "missing expiry", token: Token{Value: "token"}, want: ErrTokenExpiryRequired},
		{name: "inside refresh window", token: Token{Value: "token", ExpiresAt: now.Add(time.Minute)}, want: ErrTokenStale},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewRefreshingSource(
				SourceFunc(func(context.Context) (Token, error) { return test.token, nil }),
				RefreshOptions{RefreshBefore: 2 * time.Minute, Now: func() time.Time { return now }},
			)
			if err != nil {
				t.Fatalf("new source: %v", err)
			}
			_, err = source.Token(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("token error = %v, want %v", err, test.want)
			}
		})
	}
}
