package revision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"unicode/utf8"
)

const maxRecoveryTransitionBytes = 4096

// ErrRecoveryConflict refuses missing, unsupported, or conflicting recovery evidence.
var ErrRecoveryConflict = errors.New("authorization revision recovery evidence conflicts with retained state")

// RecoveryAuthority supplies an independently accepted recovery identity and fresh epoch.
// Neither value can come from an assumption that the restored authority remains current.
// The coordinator binds this identity to its accepted evidence and retained replay receipt.
type RecoveryAuthority struct {
	RecoveryID string `json:"recovery_id"`
	Epoch      string `json:"epoch"`
}

func (a RecoveryAuthority) valid() bool {
	return recoveryToken(a.RecoveryID) && recoveryToken(a.Epoch)
}

func recoveryToken(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char < '!' || char > '~' {
			return false
		}
	}
	return true
}

func validRecoveryStamp(stamp Stamp) bool {
	return stamp.Epoch != "" && len(stamp.Epoch) <= 256 && utf8.ValidString(stamp.Epoch) && stamp.Sequence > 0
}

func recoveryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validRecoveryDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}

func redactRecovery(state fmt.State) {
	_, _ = state.Write([]byte("<private authorization revision recovery evidence>"))
}

func encodeRecovery(value any) ([]byte, error) {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil || len(data) > maxRecoveryTransitionBytes {
		return nil, ErrRecoveryConflict
	}
	return data, nil
}

func explicitRecoveryMember(data []byte, name string) bool {
	var members map[string]jsontext.Value
	if json.Unmarshal(data, &members) != nil {
		return false
	}
	_, present := members[name]
	return present
}
