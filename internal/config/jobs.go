package config

import "time"

// JobsConfig sizes what a gateway keeps for work that outlives its request.
//
// A provider serves a finished video from a link that expires, so Starport
// fetches the bytes and answers for them itself. Bytes with no stated window
// turn a gateway into unbounded storage that no operator sized, which is what
// these three settings exist to stop.
type JobsConfig struct {
	// AssetRetention is how long a finished asset stays readable, measured from
	// the moment this gateway stored it. A caller that comes back past it reads
	// that the asset expired rather than that the job never produced one.
	AssetRetention time.Duration `env:"ASSET_RETENTION,default=24h"`

	// MaxAssetBytes bounds one stored asset. Without it a provider's decision
	// about how large its own answer is would size this deployment's storage.
	MaxAssetBytes int64 `env:"MAX_ASSET_BYTES,default=268435456"`

	// SweepInterval is how often the gateway reclaims expired asset storage. It
	// is a floor on how long expired bytes survive on disk, not on how long an
	// asset reads: an expired asset stops reading the moment it expires.
	SweepInterval time.Duration `env:"SWEEP_INTERVAL,default=1h"`
}

// The windows and bounds an absent setting selects.
const (
	// DefaultAssetRetention is a day. It is short beside the file store's month
	// on purpose: a generated video is an answer a caller collects rather than a
	// document it keeps, and both provider families publish their own links with
	// windows measured in hours.
	DefaultAssetRetention = 24 * time.Hour

	// DefaultMaxAssetBytes is 256 MiB.
	DefaultMaxAssetBytes int64 = 256 << 20

	// DefaultJobSweepInterval is how often the reclaim pass runs.
	DefaultJobSweepInterval = time.Hour
)

// AssetRetentionWindow reports how long a stored asset stays readable.
func (c *JobsConfig) AssetRetentionWindow() time.Duration {
	if c == nil || c.AssetRetention <= 0 {
		return DefaultAssetRetention
	}
	return c.AssetRetention
}

// AssetBound reports the largest asset this deployment stores.
func (c *JobsConfig) AssetBound() int64 {
	if c == nil || c.MaxAssetBytes <= 0 {
		return DefaultMaxAssetBytes
	}
	return c.MaxAssetBytes
}

// SweepEvery reports how often the reclaim pass runs.
func (c *JobsConfig) SweepEvery() time.Duration {
	if c == nil || c.SweepInterval <= 0 {
		return DefaultJobSweepInterval
	}
	return c.SweepInterval
}
