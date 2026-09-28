package localauth

import (
	"encoding/json/v2"
	"time"
)

const batchSessionPurpose = "starport.batch-session.v1"

type batchSession struct {
	Account string `json:"account"`
	Batch   string `json:"batch"`
	Session []byte `json:"session"`
}

// RetainBatchSession binds a verified session to one batch without keeping its cookie.
// The receipt uses a separate signing purpose and cannot authenticate HTTP requests.
func (g *Gate) RetainBatchSession(cookie, account, batch string, now time.Time) (string, error) {
	if g == nil || account == "" || batch == "" {
		return "", ErrBadSignature
	}
	if _, err := g.Verify(cookie, now); err != nil {
		return "", err
	}
	payload, err := unsign(g.token, sessionPurpose, cookie)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(batchSession{Account: account, Batch: batch, Session: payload})
	if err != nil {
		return "", err
	}
	return sign(g.token, batchSessionPurpose, encoded), nil
}

// VerifyBatchSession preserves the original expiry and token-rotation boundary.
func (g *Gate) VerifyBatchSession(receipt, account, batch string, now time.Time) (Session, error) {
	if g == nil || len(receipt) > 8192 {
		return Session{}, ErrBadSignature
	}
	encoded, err := unsign(g.token, batchSessionPurpose, receipt)
	if err != nil {
		return Session{}, err
	}
	var record batchSession
	if json.Unmarshal(encoded, &record) != nil || record.Account != account || record.Batch != batch || account == "" || batch == "" {
		return Session{}, ErrBadSignature
	}
	return decodeSession(record.Session, now)
}
