package catalog

import (
	"encoding/hex"
	"errors"

	"github.com/agentstation/starmap/runtime"
)

func (state fleetInventory) validate(identity runtime.FleetIdentity) error {
	if state.Version != 1 || state.Readers == nil || len(state.Entries) > fleetRetentionMaxEntries || len(state.Readers) > fleetRetentionMaxReaders {
		return errors.New("invalid fleet retention inventory")
	}
	ids := map[string]bool{}
	heads := map[runtime.FleetHead]bool{}
	var bytes int64
	generations := map[string]int64{}
	for _, blob := range state.Entries {
		if err := blob.validate(identity); err != nil {
			return err
		}
		if ids[blob.ID] || heads[blob.Head] {
			return errors.New("duplicate fleet retention identity")
		}
		if size, found := generations[blob.Head.GenerationID]; found && size != blob.GenerationBytes {
			return errors.New("fleet generation has inconsistent retained byte counts")
		}
		generations[blob.Head.GenerationID] = blob.GenerationBytes
		ids[blob.ID], heads[blob.Head] = true, true
		bytes += int64(blob.Record.Size)
	}
	for token, id := range state.Readers {
		if !fleetBlobToken(token) || !ids[id] {
			return errors.New("invalid fleet reader protection")
		}
	}
	if state.Pending != nil {
		if err := state.Pending.validate(identity); err != nil {
			return err
		}
		if ids[state.Pending.ID] || heads[state.Pending.Head] {
			return errors.New("pending deletion overlaps a retained fleet publication")
		}
		bytes += int64(state.Pending.Record.Size)
	}
	if bytes > fleetRetentionMaxBytes {
		return errors.New("fleet inventory exceeds its retained byte limit")
	}
	return nil
}

func (blob fleetBlob) validate(identity runtime.FleetIdentity) error {
	if !fleetBlobToken(blob.ID) || blob.Head == (runtime.FleetHead{}) || blob.Head.Identity != identity {
		return errors.New("invalid fleet blob ownership")
	}
	if err := blob.Head.Validate(); err != nil {
		return err
	}
	r := blob.Record
	if blob.GenerationBytes <= 0 || blob.GenerationBytes > int64(r.Size) || blob.RecoveryBytes < 0 || blob.RecoveryBytes > runtime.MaxFleetRecoveryBytes || blob.RecoveryBytes > int64(r.Size) {
		return errors.New("invalid fleet retained byte counts")
	}
	if r.Encoding != generationEncodingChunked || r.ChunkSize != generationChunkSize || r.Size <= 0 || r.Size > fleetEncodedMaxBytes || len(r.Chunks) != (r.Size+generationChunkSize-1)/generationChunkSize || !fleetChunkDigest(r.Digest) {
		return errors.New("invalid fleet blob descriptor")
	}
	for _, digest := range r.Chunks {
		if !fleetChunkDigest(digest) {
			return errors.New("invalid fleet blob chunk digest")
		}
	}
	return nil
}

func fleetBlobToken(value string) bool {
	if len(value) < 20 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			if c < 'A' || c > 'Z' {
				if c < 'a' || c > 'z' {
					return false
				}
			}
		}
	}
	return true
}

func fleetChunkDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
