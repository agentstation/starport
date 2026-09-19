package identity

import (
	"context"
	"errors"
	"fmt"
)

// ErrAccountSelectionRequired reports more than one granted account without a selection.
var ErrAccountSelectionRequired = errors.New("an explicit account selection is required")

// ResolveAccount checks at most two distinct account IDs. Empty grants never mean default access.
func (r *accountGrantRepository) ResolveAccount(ctx context.Context, userID, selected string) (string, error) {
	if !validID(userID) || (selected != "" && !validID(selected)) {
		return "", ErrMissingID
	}
	rows, err := r.db.QueryContext(ctx, r.db.Bind(`SELECT account_id FROM (
 SELECT account_id FROM account_grants WHERE user_id = ?
 UNION
 SELECT g.account_id FROM account_grants g JOIN team_memberships m ON m.team_id = g.team_id WHERE m.user_id = ?
 ) AS reachable WHERE (? = '' OR account_id = ?) ORDER BY account_id LIMIT 2`), userID, userID, selected, selected)
	if err != nil {
		return "", fmt.Errorf("resolve selected account: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var account string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&account); err != nil {
			return "", fmt.Errorf("read selected account: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("resolve selected account: %w", err)
	}
	switch count {
	case 0:
		return "", ErrAccountGrantNotFound
	case 1:
		return account, nil
	default:
		return "", ErrAccountSelectionRequired
	}
}
