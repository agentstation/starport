package localauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// TicketTTL bounds how long a launch ticket is worth anything. It covers
	// the time between a command printing a URL and a browser opening it, which
	// is a person clicking or a browser launching, and nothing else. A ticket
	// travels in a URL, and a URL is history, a shell log, and a paste, so the
	// value in it should be worthless by the time anyone reads it back.
	TicketTTL = 90 * time.Second

	// TicketParam is the query parameter /launch reads.
	TicketParam = "lt"

	// TicketLogPrefixLength is how much of a ticket may be logged. It is enough
	// to correlate one redemption with the command that minted it and far too
	// little to replay.
	TicketLogPrefixLength = 8

	ticketPurpose    = "starport.launch-ticket.v1"
	ticketNonceBytes = 16
)

var (
	// ErrTicketExpired reports a ticket this gateway signed but will no longer
	// honour.
	ErrTicketExpired = errors.New("the launch ticket has expired")
	// ErrTicketUsed reports a ticket that already opened a session.
	ErrTicketUsed = errors.New("the launch ticket has already been used")
	// ErrTicketMalformed reports a correctly signed ticket whose payload this
	// version cannot read.
	ErrTicketMalformed = errors.New("the launch ticket is not a ticket record")
)

// ticketPayload is what a ticket carries. It holds no identity, because a
// ticket does not name anyone: holding it proves only that the bearer could
// read this machine's local admin token file, and that is the whole claim.
type ticketPayload struct {
	// Nonce makes each ticket distinct, so redeeming one cannot redeem another.
	Nonce string `json:"n"`
	// ExpiresAt is unix milliseconds. It is signed rather than stored, so a
	// gateway needs no record of a ticket until someone tries to spend it.
	ExpiresAt int64 `json:"e"`
}

// MintTicket issues a one-time ticket that /launch exchanges for a console
// session.
//
// It is a pure function of the token, so the CLI mints one from the token file
// without asking the gateway. That matters for the command an operator runs
// before the gateway is listening, and it keeps the credential out of a request
// that would have had to be authenticated by something.
func MintTicket(token Token, now time.Time) (string, error) {
	if err := token.Validate(); err != nil {
		return "", err
	}
	raw := make([]byte, ticketNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes for a launch ticket: %w", err)
	}
	payload, err := json.Marshal(ticketPayload{
		Nonce:     base64.RawURLEncoding.EncodeToString(raw),
		ExpiresAt: now.Add(TicketTTL).UnixMilli(),
	})
	if err != nil {
		return "", fmt.Errorf("encode a launch ticket: %w", err)
	}
	return sign(token, ticketPurpose, payload), nil
}

// TicketPrefix is the leading fragment of a ticket that is safe to log. A whole
// ticket in a log line is a credential in a log line for as long as it lives.
func TicketPrefix(ticket string) string {
	if len(ticket) <= TicketLogPrefixLength {
		return ticket
	}
	return ticket[:TicketLogPrefixLength]
}

// Tickets enforces the one-time half of a launch ticket.
//
// The signature and the expiry live in the ticket itself; single use cannot,
// because a value that proves it has not been spent would have to be spent to
// prove it. So the gateway remembers the nonces it has honoured, and only until
// each one expires: the set never grows past the tickets minted in the last
// TicketTTL, which is a handful even on a machine whose operator is holding
// down the key.
//
// The set is per process. A restart forgets, so a ticket minted in the last
// TicketTTL and never spent could be spent once against the new process. That
// window is the ticket's own lifetime and it closes on its own, which is a
// smaller cost than persisting a record for every URL an operator prints.
type Tickets struct {
	mu    sync.Mutex
	spent map[string]time.Time
}

// NewTickets returns an empty redemption set.
func NewTickets() *Tickets {
	return &Tickets{spent: make(map[string]time.Time)}
}

// Redeem checks a ticket and spends it. Every rejection is one of the exported
// errors, and a caller that turns them all into the same HTTP answer is doing
// the right thing: which check failed is a fact about the gateway's state that
// a caller holding a bad ticket has no business learning.
func (t *Tickets) Redeem(ticket string, token Token, now time.Time) error {
	payload, err := unsign(token, ticketPurpose, ticket)
	if err != nil {
		return err
	}
	var record ticketPayload
	if err := json.Unmarshal(payload, &record); err != nil {
		return fmt.Errorf("%w: %w", ErrTicketMalformed, err)
	}
	if record.Nonce == "" || record.ExpiresAt == 0 {
		return ErrTicketMalformed
	}
	if !now.Before(time.UnixMilli(record.ExpiresAt)) {
		return ErrTicketExpired
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.forget(now)
	if _, spent := t.spent[record.Nonce]; spent {
		return ErrTicketUsed
	}
	// The nonce is remembered until the ticket would have expired anyway. Past
	// that moment the expiry check refuses it, so a longer memory would guard
	// nothing.
	t.spent[record.Nonce] = time.UnixMilli(record.ExpiresAt)
	return nil
}

// forget drops nonces whose tickets have expired. The caller holds the lock.
func (t *Tickets) forget(now time.Time) {
	for nonce, expiry := range t.spent {
		if !now.Before(expiry) {
			delete(t.spent, nonce)
		}
	}
}
