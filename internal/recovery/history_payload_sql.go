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
	case "sql_identity":
		var transition identity.RecoveryTransition
		digest, err := decodeHistoryPayload(kind, payload, &transition)
		if err != nil || !explicitIdentityChanges(payload) {
			return preparedHistorySQL{}, ErrConflict
		}
		return preparedHistorySQL{digest: digest, apply: func(ctx context.Context, db *sqlstore.DB, conn *sql.Conn) error {
			return identity.ApplyRecoveryTransition(ctx, db, conn, transition)
		}}, nil
	case "sql_authorization_final":
		var transition revision.SQLRecoveryTransition
		digest, err := decodeHistoryPayload(kind, payload, &transition)
		if err != nil {
			return preparedHistorySQL{}, err
		}
		if _, err := revision.NewSQLRecoveryTransition(nil, accepted); err != nil {
			return preparedHistorySQL{}, err
		}
		return preparedHistorySQL{digest: digest, apply: func(ctx context.Context, db *sqlstore.DB, conn *sql.Conn) error {
			before, err := revision.CaptureSQLRecovery(ctx, db, conn)
			if err != nil {
				return err
			}
			bound, err := revision.NewSQLRecoveryTransition(before, accepted)
			if err != nil {
				return err
			}
			actualDigest, err := transition.Digest()
			if err != nil {
				return err
			}
			boundDigest, err := bound.Digest()
			if err != nil || actualDigest != boundDigest {
				return revision.ErrRecoveryConflict
			}
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
