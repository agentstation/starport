package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The blob backends an operator can select. The words match the ones
// internal/blob reports from Store.Backend, so a startup line and a
// configuration file spell the same thing.
const (
	// BlobBackendFilesystem stores file bytes under a directory on this node.
	BlobBackendFilesystem = "filesystem"

	// BlobBackendObjectStore stores file bytes in an S3-compatible object
	// store. One client reaches AWS S3, Cloudflare R2, MinIO, and Backblaze B2.
	BlobBackendObjectStore = "objectstore"
)

// ErrIncompleteBlobConfig reports an object store selection that names too
// little to reach a bucket.
//
// An incomplete selection refuses startup rather than falling back to the
// filesystem. A deployment that asked for a shared store and silently got a
// per-node directory would serve a file on one node and a not-found result on
// the next, and nothing in the request path would say why.
var ErrIncompleteBlobConfig = errors.New("config: the object store configuration is incomplete")

// FilesConfig selects where uploaded file bytes land.
type FilesConfig struct {
	// Backend names the store. An absent value selects the filesystem, which
	// needs nothing configured and serves one node.
	Backend string `env:"BACKEND,default=filesystem"`

	// Path roots the filesystem backend. An absent value puts the objects in
	// the platform data directory, beside the record store.
	Path string `env:"PATH"`

	// MaxUploadBytes bounds one upload. It is a deployment decision rather
	// than a wire-format one: the same request that a shared object store
	// absorbs can fill the disk of a single node. FIL6 adds the separate
	// bound on what one account may keep stored at once.
	MaxUploadBytes int64 `env:"MAX_UPLOAD_BYTES,default=536870912"`

	// Retention is how long a stored file stays readable. Every file expires,
	// and an upload may ask for a shorter window but never a longer one.
	Retention time.Duration `env:"RETENTION,default=720h"`

	// SweepInterval is how often the gateway reclaims expired and abandoned
	// files. It is a floor on how long deleted bytes survive, not on how long
	// a file reads: an expired file reads as not found the moment it expires.
	SweepInterval time.Duration `env:"SWEEP_INTERVAL,default=1h"`

	ObjectStore ObjectStoreConfig `env:",prefix=OBJECT_STORE_"`
}

// DefaultMaxUploadBytes is the upload bound an absent setting selects. It
// matches the cap OpenAI publishes, so an SDK that already refuses a larger
// file refuses it before the request leaves the client.
const DefaultMaxUploadBytes int64 = 512 << 20

// UploadBound reports the bound one upload may reach, with the default
// standing in for an absent or nonsense value.
func (c *FilesConfig) UploadBound() int64 {
	if c == nil || c.MaxUploadBytes <= 0 {
		return DefaultMaxUploadBytes
	}
	return c.MaxUploadBytes
}

// DefaultRetention is the window an absent setting selects, and
// DefaultSweepInterval is how often the reclaim pass runs.
const (
	DefaultRetention     = 30 * 24 * time.Hour
	DefaultSweepInterval = time.Hour
)

// RetentionWindow reports how long a stored file stays readable.
func (c *FilesConfig) RetentionWindow() time.Duration {
	if c == nil || c.Retention <= 0 {
		return DefaultRetention
	}
	return c.Retention
}

// SweepEvery reports how often the reclaim pass runs.
func (c *FilesConfig) SweepEvery() time.Duration {
	if c == nil || c.SweepInterval <= 0 {
		return DefaultSweepInterval
	}
	return c.SweepInterval
}

// ObjectStoreConfig addresses one S3-compatible bucket.
//
// The two key fields carry the `secret` tag, so Redacted removes them from
// every inspection the console and the CLI print. No error this package
// returns names them either.
type ObjectStoreConfig struct {
	// Bucket names the bucket. It is the one field with no default.
	Bucket string `env:"BUCKET"`

	// Region names the region. AWS S3 needs it. Another implementation
	// usually accepts any value beside an explicit endpoint.
	Region string `env:"REGION"`

	// Endpoint addresses an implementation other than AWS S3. An absent value
	// selects the AWS endpoint for the region.
	Endpoint string `env:"ENDPOINT" redact:"url"`

	// Prefix scopes every key this deployment writes, so one bucket can hold
	// more than one deployment.
	Prefix string `env:"PREFIX"`

	// AccessKeyID and SecretAccessKey state static credentials. Both absent
	// selects the ambient AWS credential chain, which is the usual choice on
	// an instance that carries a role.
	AccessKeyID     string `env:"ACCESS_KEY_ID" secret:"true"`
	SecretAccessKey string `env:"SECRET_ACCESS_KEY" secret:"true"`
}

// SelectedBackend reports the backend this configuration names, with the
// filesystem standing in for an absent value.
func (c *FilesConfig) SelectedBackend() string {
	if c == nil || strings.TrimSpace(c.Backend) == "" {
		return BlobBackendFilesystem
	}
	return strings.ToLower(strings.TrimSpace(c.Backend))
}

// Validate reports a selection this build cannot open.
func (c *FilesConfig) Validate() error {
	switch c.SelectedBackend() {
	case BlobBackendFilesystem:
		return nil
	case BlobBackendObjectStore:
		return c.ObjectStore.validate()
	default:
		return fmt.Errorf("files backend %q is not one of %q or %q",
			c.Backend, BlobBackendFilesystem, BlobBackendObjectStore)
	}
}

// validate reports the fields an object store selection must name.
//
// It names the missing setting and never the credentials. An error that
// printed a key to explain a missing bucket would put the key in every log
// that captured the startup failure.
func (c *ObjectStoreConfig) validate() error {
	var missing []string
	if strings.TrimSpace(c.Bucket) == "" {
		missing = append(missing, "bucket")
	}
	if strings.TrimSpace(c.Region) == "" && strings.TrimSpace(c.Endpoint) == "" {
		missing = append(missing, "region or endpoint")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: it names no %s", ErrIncompleteBlobConfig, strings.Join(missing, " and no "))
	}
	// One key without the other is a typo rather than a choice. Both absent
	// selects the ambient credential chain, which is a real configuration.
	if (strings.TrimSpace(c.AccessKeyID) == "") != (strings.TrimSpace(c.SecretAccessKey) == "") {
		return fmt.Errorf("%w: it states one half of a static credential pair", ErrIncompleteBlobConfig)
	}
	return nil
}
