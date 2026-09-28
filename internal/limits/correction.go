package limits

import (
	"errors"
	"time"
)

// CorrectionHorizon bounds new corrections from the original charge settlement.
// It never expires unresolved reservations or accepted audit evidence.
const CorrectionHorizon = 90 * 24 * time.Hour

// ErrCorrectionExpired refuses a new correction after its original deadline.
var ErrCorrectionExpired = errors.New("the settled-charge correction deadline passed")
