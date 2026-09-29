package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// InspectSelectionPolicyPublications selects verified staging evidence for inactive retention.
// The caller preserves selected bytes and validates the remaining policy through its owner.
// This check never accepts candidate policy or grants credential use.
func InspectSelectionPolicyPublications(ctx context.Context, files map[string]productfiles.RetainedFile, read productfiles.RetainedRecordReader) ([]string, error) {
	return productfiles.InspectRetainedPublications(ctx, files, read, func(parent, destination, prefix string) bool {
		if parent != "." || prefix != ".product-stage-" {
			return false
		}
		if destination == "policy.json" {
			return true
		}
		digest, ok := strings.CutPrefix(destination, "provider-")
		if !ok {
			return false
		}
		digest, ok = strings.CutSuffix(digest, ".json")
		decoded, err := hex.DecodeString(digest)
		return ok && err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == digest
	})
}
