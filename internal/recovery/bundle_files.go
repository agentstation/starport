package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/credentials"
)

const bundleMaxManifestBytes = 16 << 20
const bundleMaxArtifacts = 100000

func validBundlePath(name string) bool {
	if !fs.ValidPath(name) || name == "." || len(name) > 4096 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		// Use portable names on every target. Source names remain outside this format.
		if part == "" || len(part) > 128 {
			return false
		}
		for _, c := range part {
			allowed := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
			if !allowed {
				return false
			}
		}
		upper := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if strings.HasSuffix(part, ".") || slices.Contains([]string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}, upper) {
			return false
		}
	}
	return true
}

func copyBundleFile(ctx context.Context, root *os.Root, selected BundleFile) (resultErr error) {
	info, err := os.Lstat(selected.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("backup input must be a regular file")
	}
	input, err := os.Open(selected.Path) // #nosec G304 -- the coordinator copies selected local files into private storage and verifies their identity.
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	opened, err := input.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return errors.New("backup input identity changed")
	}
	name := path.Join("files", selected.ID)
	if err := root.MkdirAll(path.Dir(name), 0o700); err != nil {
		return err
	}
	output, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
	digest := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(output, digest), &kvSnapshotReader{ctx: ctx, reader: input}, opened.Size()); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.New("backup input size changed")
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return errors.New("backup input changed during capture")
	}
	if selected.ExpectedSHA256 != "" && hex.EncodeToString(digest.Sum(nil)) != selected.ExpectedSHA256 {
		return errors.New("backup configuration changed after selection")
	}
	return output.Sync()
}

func inspectBundleArtifacts(ctx context.Context, root *os.Root) (artifacts []BundleArtifact, resultErr error) {
	entries := 0
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 2*bundleMaxArtifacts {
			return errors.New("backup exceeds the directory-entry limit")
		}
		if entry.IsDir() {
			return nil
		}
		if name == bundleManifestFile {
			return nil
		}
		if !entry.Type().IsRegular() || !validBundlePath(name) {
			return errors.New("backup contains an unsupported file")
		}
		if slices.Contains([]string{".record-publications/.owner.lock", "kv/.record-publications/.owner.lock", "sql/.record-publications/.owner.lock"}, name) {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() != 0 {
				return errors.New("backup publication lock has unexpected content")
			}
			return nil
		}
		if len(artifacts) >= bundleMaxArtifacts {
			return errors.New("backup exceeds the artifact limit")
		}
		artifact, err := inspectBundleArtifact(ctx, root, name)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifacts, nil
}

func inspectBundleArtifact(ctx context.Context, root *os.Root, name string) (artifact BundleArtifact, resultErr error) {
	file, err := root.Open(name)
	if err != nil {
		return artifact, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return artifact, err
	}
	if !info.Mode().IsRegular() {
		return artifact, errors.New("backup artifact must be regular")
	}
	digest := sha256.New()
	count, err := io.Copy(digest, &kvSnapshotReader{ctx: ctx, reader: file})
	if err != nil {
		return artifact, err
	}
	if count != info.Size() {
		return artifact, errors.New("backup artifact size changed")
	}
	return BundleArtifact{Path: name, Size: count, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func (m BundleManifest) validate() error {
	if m.Format != bundleFormat {
		return errors.New("unsupported backup format")
	}
	if err := m.Request.validate(); err != nil {
		return err
	}
	if m.StartedAt.IsZero() || m.FinishedAt.Before(m.StartedAt) || len(m.Artifacts) < 5 || len(m.Artifacts) > bundleMaxArtifacts || m.KeyChallenge == "" {
		return errors.New("backup manifest is incomplete")
	}
	required := map[string]BundleArtifact{
		bundleKVFile:   {Path: bundleKVFile, Size: m.KV.Size, SHA256: m.KV.SHA256},
		bundleSQLFile:  {Path: bundleSQLFile, Size: m.SQL.Size, SHA256: m.SQL.SHA256},
		bundleBlobFile: {Path: bundleBlobFile, Size: m.Blobs.Size, SHA256: m.Blobs.SHA256},
	}
	seen := make(map[string]bool, len(m.Artifacts))
	for _, artifact := range m.Artifacts {
		hash, err := hex.DecodeString(artifact.SHA256)
		if err != nil || len(hash) != sha256.Size || artifact.Size < 0 || !validBundlePath(artifact.Path) || artifact.Path == bundleManifestFile || seen[artifact.Path] {
			return errors.New("backup manifest contains an invalid artifact")
		}
		if !strings.HasPrefix(artifact.Path, "files/") && !slices.Contains([]string{bundleKVFile, "kv/snapshot.json", bundleSQLFile, "sql/snapshot.json", bundleBlobFile}, artifact.Path) {
			return errors.New("backup manifest contains an unknown component")
		}
		if expected, ok := required[artifact.Path]; ok && artifact != expected {
			return errors.New("backup component receipt differs from its artifact")
		}
		seen[artifact.Path] = true
	}
	for _, name := range []string{bundleKVFile, "kv/snapshot.json", bundleSQLFile, "sql/snapshot.json", bundleBlobFile} {
		if !seen[name] {
			return errors.New("backup manifest omits a required component")
		}
	}
	return nil
}

// VerifyBundle verifies retained files and encryption-key access before any restore writes.
// It grants no recovery approval and cannot infer later revocations or spending.
func VerifyBundle(ctx context.Context, directory, expectedDigest string, encryption *credentials.EncryptionService) (manifest BundleManifest, resultErr error) {
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	if encryption == nil {
		return manifest, errors.New("backup verification requires encryption-key access")
	}
	selected, err := productfiles.ExistingDirectory(directory)
	if err != nil {
		return manifest, err
	}
	root, err := selected.Open()
	if err != nil {
		return manifest, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	info, err := root.Lstat(bundleManifestFile)
	if err != nil {
		return manifest, err
	}
	if !info.Mode().IsRegular() || info.Size() > bundleMaxManifestBytes {
		return manifest, errors.New("backup manifest has an invalid size or file type")
	}
	file, err := root.Open(bundleManifestFile)
	if err != nil {
		return manifest, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, bundleMaxManifestBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return manifest, err
	}
	if len(body) > bundleMaxManifestBytes {
		return manifest, errors.New("backup manifest exceeds its size limit")
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != expectedDigest {
		return BundleManifest{}, errors.New("backup manifest differs from the independently retained digest")
	}
	if err := json.Unmarshal(body, &manifest, json.RejectUnknownMembers(true)); err != nil {
		return BundleManifest{}, err
	}
	if err := manifest.validate(); err != nil {
		return BundleManifest{}, err
	}
	challenge, err := encryption.DecryptCredential(manifest.KeyChallenge)
	if err != nil || challenge != bundleKeyChallenge+manifest.Request.OperationID {
		return BundleManifest{}, errors.New("backup encryption-key access could not be verified")
	}
	artifacts, err := inspectBundleArtifacts(ctx, root)
	if err != nil {
		return BundleManifest{}, err
	}
	if !slices.Equal(artifacts, manifest.Artifacts) {
		return BundleManifest{}, errors.New("backup artifacts differ from the complete manifest")
	}
	return manifest, nil
}

func syncBundleDirectories(ctx context.Context, root *os.Root) error {
	var directories []string
	if err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, name)
		}
		return nil
	}); err != nil {
		return err
	}
	slices.Reverse(directories)
	for _, name := range directories {
		directory, err := root.OpenRoot(name)
		if err != nil {
			return err
		}
		if err := errors.Join(productfiles.SyncDirectory(directory), directory.Close()); err != nil {
			return err
		}
	}
	return nil
}
