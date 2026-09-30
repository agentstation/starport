package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strings"
)

const recoveryCensusWitnessTable = "catalog_recovery"

// RelationalRecoveryCensus separates portable domain rows from native recovery controls.
// Every row contributes to one digest. These diagnostics grant no recovery authority.
type RelationalRecoveryCensus struct {
	DomainSHA256   string `json:"domain_sha256"`
	WitnessSHA256  string `json:"witness_sha256"`
	ControlSHA256  string `json:"control_sha256"`
	RevisionSHA256 string `json:"revision_sha256"`
	DomainRows     int64  `json:"domain_rows"`
	WitnessRows    int64  `json:"witness_rows"`
	ControlRows    int64  `json:"control_rows"`
	RevisionRows   int64  `json:"revision_rows"`
	AuditHighWater int64  `json:"audit_high_water"`
}

// RecoveryCensus streams the complete compiled row contract from an immutable image.
// It preserves original native controls as separate facts that need owner validation.
func (v *RelationalSnapshotView) RecoveryCensus(ctx context.Context) (result RelationalRecoveryCensus, resultErr error) {
	if ctx == nil || v == nil || v.db == nil {
		return result, ErrClosed
	}
	if err := validateRelationalSchema(ctx, v.db, TypeSQLite); err != nil {
		return result, err
	}
	domain, witness, controls, revisions := sha256.New(), sha256.New(), sha256.New(), sha256.New()
	for _, table := range relationalTables {
		rows, err := v.db.QueryContext(ctx, "SELECT "+table.columnNames()+" FROM "+table.name+" ORDER BY "+table.columnNames()) // #nosec G202 -- The compiled portable contract owns every identifier.
		if err != nil {
			return RelationalRecoveryCensus{}, err
		}
		for rows.Next() {
			values, err := scanRelationalRow(rows, table, TypeSQLite)
			if err != nil {
				return RelationalRecoveryCensus{}, errors.Join(err, rows.Close())
			}
			target := domain
			switch {
			case table.name == "authorization_revision":
				target = revisions
				result.RevisionRows++
			case table.name == recoveryCensusWitnessTable:
				target = witness
				result.WitnessRows++
			case table.name == metadataTable && recoveryControlName(values[0].(string)):
				target = controls
				result.ControlRows++
			default:
				result.DomainRows++
			}
			// Each encoded pair has a JSON boundary. No raw row bytes enter diagnostics.
			if err := json.MarshalWrite(target, []any{table.name, values}, json.Deterministic(true)); err != nil {
				return RelationalRecoveryCensus{}, errors.Join(err, rows.Close())
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return RelationalRecoveryCensus{}, err
		}
	}
	high, err := auditHighWater(ctx, v.db, TypeSQLite)
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	if err := json.MarshalWrite(domain, []any{"audit_allocation", high}); err != nil {
		return RelationalRecoveryCensus{}, err
	}
	result.AuditHighWater = high
	result.DomainSHA256 = hex.EncodeToString(domain.Sum(nil))
	result.WitnessSHA256 = hex.EncodeToString(witness.Sum(nil))
	result.ControlSHA256 = hex.EncodeToString(controls.Sum(nil))
	result.RevisionSHA256 = hex.EncodeToString(revisions.Sum(nil))
	return result, nil
}

func recoveryControlName(name string) bool {
	return name == relationalImportMarker || name == relationalActivationCurrent || name == relationalReplayCurrent ||
		strings.HasPrefix(name, relationalActivationPrefix) || strings.HasPrefix(name, "relational-replay-v1:") ||
		strings.HasPrefix(name, "relational-reconciliation-v1:")
}
