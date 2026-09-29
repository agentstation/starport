package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// blobAddress gives both backends the same recoverable physical identity.
// Retirement markers remain addressable even after their logical records disappear.
func blobAddress(namespace, key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])
	return namespace + "/" + name[:2] + "/" + name[2:4] + "/" + name
}

func validBlobAddress(address string) bool {
	if validActivationAddress(address) {
		return true
	}
	parts := strings.Split(address, "/")
	if len(parts) != 4 || (parts[0] != objectsDir && parts[0] != retainedDir) || len(parts[3]) != 64 {
		return false
	}
	if parts[1] != parts[3][:2] || parts[2] != parts[3][2:4] {
		return false
	}
	for _, c := range parts[3] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
