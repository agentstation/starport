package recovery

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
)

type preparedHistorySQL struct {
	digest string
	apply  func(context.Context, *sqlstore.DB, *sql.Conn) error
}

func (preparedHistorySQL) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private prepared SQL recovery>"))
}

// prepareHistorySQL returns an owner callback for the coordinator's closed replay transaction.
// It does not open a connection or apply, commit, or activate any state.
func prepareHistorySQL(kind string, payload []byte, accepted revision.RecoveryAuthority) (preparedHistorySQL, error) {
	switch kind {
	case historySQLIdentity:
		var transition identity.RecoveryTransition
		digest, err := decodeHistoryPayload(kind, payload, &transition)
		if err != nil || !explicitIdentityChanges(payload) {
			return preparedHistorySQL{}, ErrConflict
		}
		return preparedHistorySQL{digest: digest, apply: func(ctx context.Context, db *sqlstore.DB, conn *sql.Conn) error {
			return identity.ApplyRecoveryTransition(ctx, db, conn, transition)
		}}, nil
	case historySQLAuthorityFinal:
		var input historySQLAuthorityPayload
		_, err := decodeHistoryPayload(kind, payload, &input)
		if err != nil || input.Version != 1 || !explicitHistoryMembers(payload, "expected") {
			return preparedHistorySQL{}, ErrConflict
		}
		transition, err := revision.NewSQLRecoveryTransition(input.Expected, accepted)
		if err != nil {
			return preparedHistorySQL{}, err
		}
		digest, err := transition.Digest()
		if err != nil {
			return preparedHistorySQL{}, err
		}
		return preparedHistorySQL{digest: digest, apply: func(ctx context.Context, db *sqlstore.DB, conn *sql.Conn) error {
			return revision.ApplySQLRecovery(ctx, db, conn, transition)
		}}, nil
	default:
		return preparedHistorySQL{}, ErrConflict
	}
}
func explicitIdentityChanges(payload []byte) bool {
	var object map[string]jsontext.Value
	if json.Unmarshal(payload, &object) != nil {
		return false
	}
	var events []map[string]jsontext.Value
	if json.Unmarshal(object["events"], &events) != nil {
		return false
	}
	for _, event := range events {
		for _, change := range event {
			if !explicitHistoryMembers(change, "before", "after") {
				return false
			}
		}
	}
	return true
}

type historySQLAuthorityPayload struct {
	Version  int             `json:"version"`
	Expected *revision.Stamp `json:"expected"`
}
