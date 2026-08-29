// Package identity owns the humans a deployment knows: users, the teams
// they form, and the memberships that tie them together. A user arrives
// through an acquisition path — OAuth or enterprise SSO — that resolves an
// external subject to the one user model here; this package holds the
// models and their durable repositories. The routes that authenticate a
// person live with the identity grant, not here.
//
// Identity is optional. A deployment with no identity configured has no
// rows here, and every account works exactly as it does without users.
package identity

import (
	"errors"
	"time"
)

// User is one human the deployment knows. The subject is the external
// identity an acquisition path resolved — the provider-qualified subject
// an OAuth or SSO callback names — and it is unique: the same subject
// returning is the same user.
type User struct {
	ID string `json:"id"`
	// Subject is the provider-qualified external identity subject, such as
	// "google:114380...". It never changes for the life of the user.
	Subject     string    `json:"subject"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Team is a named group of users. Access granted to a team reaches every
// member, so a team is the unit an operator manages instead of people.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Membership ties one user to one team. It carries no state of its own
// beyond when it was made: what a membership grants comes from what the
// team is granted.
type Membership struct {
	UserID    string    `json:"user_id"`
	TeamID    string    `json:"team_id"`
	CreatedAt time.Time `json:"created_at"`
}

// maxIDLength bounds every identity ID and the external subject. The bound
// is the MySQL indexed-column width, so an ID that validates here fits
// every backend the sqlstore serves.
const maxIDLength = 191

// maxNameLength bounds the human-readable fields.
const maxNameLength = 255

var (
	// ErrMissingID reports an empty or oversized identifier.
	ErrMissingID = errors.New("missing id")
	// ErrMissingSubject reports a user without an external subject.
	ErrMissingSubject = errors.New("missing external identity subject")
	// ErrInvalidName reports an empty or oversized human-readable name.
	ErrInvalidName = errors.New("invalid name: must be 1-255 characters")
	// ErrInvalidTimestamps reports an update that precedes creation.
	ErrInvalidTimestamps = errors.New("updated_at must not be before created_at")

	// ErrUserNotFound reports a missing user.
	ErrUserNotFound = errors.New("user not found")
	// ErrUserConflict reports an existing user, a taken subject, or a
	// stale revision.
	ErrUserConflict = errors.New("user revision conflict")
	// ErrCorruptUser reports invalid durable user data.
	ErrCorruptUser = errors.New("user record is invalid")

	// ErrTeamNotFound reports a missing team.
	ErrTeamNotFound = errors.New("team not found")
	// ErrTeamConflict reports an existing team or a stale revision.
	ErrTeamConflict = errors.New("team revision conflict")
	// ErrCorruptTeam reports invalid durable team data.
	ErrCorruptTeam = errors.New("team record is invalid")

	// ErrMembershipNotFound reports a membership that does not exist.
	ErrMembershipNotFound = errors.New("membership not found")
	// ErrMembershipConflict reports a membership that already exists.
	ErrMembershipConflict = errors.New("membership already exists")
)

func validID(value string) bool {
	return value != "" && len(value) <= maxIDLength
}

// Validate checks the user invariants.
func (u User) Validate() error {
	if !validID(u.ID) {
		return ErrMissingID
	}
	if !validID(u.Subject) {
		return ErrMissingSubject
	}
	if len(u.Email) > maxNameLength {
		return ErrInvalidName
	}
	if len(u.DisplayName) > maxNameLength {
		return ErrInvalidName
	}
	if !u.UpdatedAt.IsZero() && u.UpdatedAt.Before(u.CreatedAt) {
		return ErrInvalidTimestamps
	}
	return nil
}

// Validate checks the team invariants.
func (t Team) Validate() error {
	if !validID(t.ID) {
		return ErrMissingID
	}
	if t.Name == "" || len(t.Name) > maxNameLength {
		return ErrInvalidName
	}
	if !t.UpdatedAt.IsZero() && t.UpdatedAt.Before(t.CreatedAt) {
		return ErrInvalidTimestamps
	}
	return nil
}

// Validate checks the membership invariants.
func (m Membership) Validate() error {
	if !validID(m.UserID) || !validID(m.TeamID) {
		return ErrMissingID
	}
	return nil
}
