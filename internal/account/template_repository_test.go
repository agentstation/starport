package account

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/sqlstore"
)

// newTemplateTestRepository opens an in-memory relational store, migrates
// it, and returns a repository on it — the same composition the runtime
// builds, minus the file.
func newTemplateTestRepository(t *testing.T) TemplateRepository {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenTemplates(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

// TestTemplateRepositoryCRUD proves the durable contract: what was created
// is what reads back, an update moves the revision, and a delete removes
// the row.
func TestTemplateRepositoryCRUD(t *testing.T) {
	repository := newTemplateTestRepository(t)
	ctx := context.Background()

	created, err := repository.Create(ctx, Template{
		ID:                 "team-default",
		Name:               "Team default",
		CredentialStrategy: StrategyBYOKFirst,
		Limits:             &limits.Limits{Requests: &limits.RequestLimit{Limit: 60, WindowSeconds: 60}},
		BYOKPolicy:         &BYOKPolicy{Mode: BYOKSelected, Providers: []string{"groq"}},
		Access:             []ProviderAccess{{Provider: "groq"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("create revision = %d", created.Revision)
	}
	if created.Template.CreatedAt.IsZero() || created.Template.UpdatedAt.IsZero() {
		t.Fatal("create must stamp timestamps")
	}

	read, err := repository.GetByID(ctx, "team-default")
	if err != nil {
		t.Fatal(err)
	}
	if read.Template.Name != "Team default" || read.Template.CredentialStrategy != StrategyBYOKFirst {
		t.Fatalf("read back %+v", read.Template)
	}
	if read.Template.BYOKPolicy == nil || read.Template.BYOKPolicy.Providers[0] != "groq" {
		t.Fatal("policy did not survive the round trip")
	}
	if read.Template.Limits == nil || read.Template.Limits.Requests.Limit != 60 {
		t.Fatal("limits did not survive the round trip")
	}

	edited := read.Template
	edited.Name = "Team default v2"
	updated, err := repository.Update(ctx, edited, read.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("update revision = %d", updated.Revision)
	}
	if !updated.Template.CreatedAt.Equal(read.Template.CreatedAt) {
		t.Fatal("update must preserve creation time")
	}

	listed, err := repository.List(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Template.Name != "Team default v2" {
		t.Fatalf("list = %+v", listed)
	}

	if err := repository.Delete(ctx, "team-default", updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(ctx, "team-default"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("after delete GetByID = %v", err)
	}
}

// TestTemplateRepositoryConflicts proves the two conflicts: a duplicate
// create and a stale-revision update. Both refuse instead of overwriting.
func TestTemplateRepositoryConflicts(t *testing.T) {
	repository := newTemplateTestRepository(t)
	ctx := context.Background()

	first, err := repository.Create(ctx, Template{ID: "org", Name: "Org"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Create(ctx, Template{ID: "org", Name: "Org again"}); !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("duplicate create = %v", err)
	}

	edited := first.Template
	edited.Name = "Org v2"
	if _, err := repository.Update(ctx, edited, first.Revision+7); !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("stale update = %v", err)
	}
	if err := repository.Delete(ctx, "org", first.Revision+7); !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("stale delete = %v", err)
	}

	// The refused writes changed nothing.
	read, err := repository.GetByID(ctx, "org")
	if err != nil {
		t.Fatal(err)
	}
	if read.Template.Name != "Org" || read.Revision != 1 {
		t.Fatalf("record moved after refused writes: %+v", read)
	}
}

// TestTemplateRepositoryRefusesInvalid proves validation happens on write:
// an invalid template never reaches a row.
func TestTemplateRepositoryRefusesInvalid(t *testing.T) {
	repository := newTemplateTestRepository(t)
	ctx := context.Background()

	if _, err := repository.Create(ctx, Template{ID: "bad", Name: "Bad",
		BYOKPolicy: &BYOKPolicy{Mode: BYOKSelected}}); !errors.Is(err, ErrInvalidBYOKPolicy) {
		t.Fatalf("invalid create = %v", err)
	}
	if _, err := repository.GetByID(ctx, "bad"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("refused create left a row: %v", err)
	}

	created, err := repository.Create(ctx, Template{ID: "good", Name: "Good"})
	if err != nil {
		t.Fatal(err)
	}
	edited := created.Template
	edited.CredentialStrategy = "sometimes"
	if _, err := repository.Update(ctx, edited, created.Revision); !errors.Is(err, ErrInvalidCredentialStrategy) {
		t.Fatalf("invalid update = %v", err)
	}
}

// TestTemplateRepositoryListPaginates proves stable id-ordered pagination.
func TestTemplateRepositoryListPaginates(t *testing.T) {
	repository := newTemplateTestRepository(t)
	ctx := context.Background()

	for _, id := range []string{"c-team", "a-team", "b-team"} {
		if _, err := repository.Create(ctx, Template{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}

	firstPage, err := repository.List(ctx, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage) != 2 || firstPage[0].Template.ID != "a-team" || firstPage[1].Template.ID != "b-team" {
		t.Fatalf("first page = %+v", firstPage)
	}
	secondPage, err := repository.List(ctx, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage) != 1 || secondPage[0].Template.ID != "c-team" {
		t.Fatalf("second page = %+v", secondPage)
	}
}

// TestOpenTemplatesRefusesNil keeps the loud-degradation contract: a nil
// store is refused at composition, not discovered on the first request.
func TestOpenTemplatesRefusesNil(t *testing.T) {
	if _, err := OpenTemplates(nil); !errors.Is(err, ErrTemplateRepositoryRequired) {
		t.Fatalf("OpenTemplates(nil) = %v", err)
	}
}
