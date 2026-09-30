package localauth

import "encoding/json"

// CheckRecoveryToken verifies a retained target token without creating or rotating it.
// The caller must check native file access before supplying these private bytes.
func CheckRecoveryToken(body []byte, bindHost string) error {
	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return ErrCorruptRecord
	}
	if err := token.Validate(); err != nil {
		return err
	}
	if !AllowsExposure(bindHost, token) {
		return ErrCorruptRecord
	}
	return nil
}
