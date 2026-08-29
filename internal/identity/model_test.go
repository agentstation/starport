package identity

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUserValidate(t *testing.T) {
	valid := User{ID: "u-1", Subject: "google:114380", Email: "a@example.com", DisplayName: "Ada"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		user User
		want error
	}{
		{"missing id", User{Subject: "google:1"}, ErrMissingID},
		{"oversized id", User{ID: strings.Repeat("a", 192), Subject: "google:1"}, ErrMissingID},
		{"missing subject", User{ID: "u-1"}, ErrMissingSubject},
		{"oversized subject", User{ID: "u-1", Subject: strings.Repeat("s", 192)}, ErrMissingSubject},
		{"oversized email", User{ID: "u-1", Subject: "google:1", Email: strings.Repeat("e", 256)}, ErrInvalidName},
		{"oversized display name", User{ID: "u-1", Subject: "google:1", DisplayName: strings.Repeat("d", 256)}, ErrInvalidName},
		{
			"update before creation",
			User{ID: "u-1", Subject: "google:1", CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(50, 0)},
			ErrInvalidTimestamps,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.user.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTeamValidate(t *testing.T) {
	if err := (Team{ID: "t-1", Name: "Platform"}).Validate(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		team Team
		want error
	}{
		{"missing id", Team{Name: "Platform"}, ErrMissingID},
		{"missing name", Team{ID: "t-1"}, ErrInvalidName},
		{"oversized name", Team{ID: "t-1", Name: strings.Repeat("n", 256)}, ErrInvalidName},
		{
			"update before creation",
			Team{ID: "t-1", Name: "Platform", CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(50, 0)},
			ErrInvalidTimestamps,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.team.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMembershipValidate(t *testing.T) {
	if err := (Membership{UserID: "u-1", TeamID: "t-1"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Membership{TeamID: "t-1"}).Validate(); !errors.Is(err, ErrMissingID) {
		t.Fatalf("missing user id must refuse, got %v", err)
	}
	if err := (Membership{UserID: "u-1"}).Validate(); !errors.Is(err, ErrMissingID) {
		t.Fatalf("missing team id must refuse, got %v", err)
	}
}
