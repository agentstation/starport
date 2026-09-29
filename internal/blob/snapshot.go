package blob

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const blobSnapshotFormat = "starport-blob-tar-v1"

// Snapshot identifies committed bytes, retirement markers, and activation history.
// Objects counts archive entries, including historical activation receipts.
// It excludes incomplete uploads, native activation authority, and noncurrent versions.
type Snapshot struct {
	Format  string `json:"format"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Objects int64  `json:"objects"`
	Retired int64  `json:"retired"`
}

// Backup snapshots the selected blob namespace. The caller must fence all writers.
// The destination must be a new file beneath an existing private directory.
// An error can leave a partial file, which has no valid returned receipt.
func Backup(ctx context.Context, store Store, destination string) (Snapshot, error) {
	switch source := store.(type) {
	case *Filesystem:
		return writeBlobSnapshot(ctx, destination, source.walkObjects)
	case *ObjectStore:
		if err := source.probeLayout(ctx); err != nil {
			return Snapshot{}, err
		}
		return writeBlobSnapshot(ctx, destination, func(ctx context.Context, yield blobObjectVisitor) error { return source.walkObjects(ctx, false, yield) })
	default:
		return Snapshot{}, errors.New("blob: backup requires a native storage adapter")
	}
}

// BackupLegacyObjects reads the former logical-key layout without changing it.
// Migration restores the resulting image into a different, empty prefix or directory.
func BackupLegacyObjects(ctx context.Context, source *ObjectStore, destination string) (Snapshot, error) {
	if source == nil {
		return Snapshot{}, errors.New("blob: migration requires object storage")
	}
	if err := source.readLayout(ctx); !isAbsent(err) {
		if err != nil {
			return Snapshot{}, err
		}
		return Snapshot{}, errors.New("blob: selected storage already uses the portable layout")
	}
	return writeBlobSnapshot(ctx, destination, func(ctx context.Context, yield blobObjectVisitor) error { return source.walkObjects(ctx, true, yield) })
}

type blobObjectVisitor func(string, int64, io.Reader) error
type blobObjectWalker func(context.Context, blobObjectVisitor) error

func writeBlobSnapshot(ctx context.Context, destination string, walk blobObjectWalker) (receipt Snapshot, resultErr error) {
	defer func() {
		if resultErr != nil {
			receipt = Snapshot{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return receipt, errors.New("blob: backup requires a clean absolute path")
	}
	dir, err := productfiles.ExistingDirectory(filepath.Dir(destination))
	if err != nil {
		return receipt, err
	}
	root, err := dir.Open()
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, err := root.OpenFile(filepath.Base(destination), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	hash := sha256.New()
	archive := tar.NewWriter(io.MultiWriter(file, hash))
	err = walk(ctx, func(address string, size int64, input io.Reader) error {
		if !validBlobAddress(address) || size < 0 {
			return errors.New("blob: invalid snapshot address or size")
		}
		reader, retired, err := inspectBlobEnvelope(address, size, input)
		if err != nil {
			return err
		}
		if err := archive.WriteHeader(&tar.Header{Name: address, Mode: 0o600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if _, err := io.CopyN(archive, &contextReader{ctx: ctx, r: reader}, size); err != nil {
			return err
		}
		var extra [1]byte
		if n, err := reader.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
			return errors.New("blob: object changed during backup")
		}
		receipt.Objects++
		if retired {
			receipt.Retired++
		}
		return nil
	})
	if err = errors.Join(err, archive.Close()); err != nil {
		return Snapshot{}, err
	}
	if err := file.Sync(); err != nil {
		return Snapshot{}, err
	}
	if err := productfiles.SyncDirectory(root); err != nil {
		return Snapshot{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	receipt.Format, receipt.Size, receipt.SHA256 = blobSnapshotFormat, info.Size(), hex.EncodeToString(hash.Sum(nil))
	return receipt, nil
}

func inspectBlobEnvelope(address string, size int64, input io.Reader) (io.Reader, bool, error) {
	if validActivationAddress(address) {
		return inspectActivationHistory(address, size, input)
	}
	if strings.HasPrefix(address, objectsDir+"/") {
		return input, false, nil
	}
	if size < int64(len(liveEnvelope)) {
		return nil, false, ErrCorruptPublication
	}
	var header [len(liveEnvelope)]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return nil, false, errors.Join(ErrCorruptPublication, err)
	}
	switch string(header[:]) {
	case liveEnvelope:
		return io.MultiReader(strings.NewReader(liveEnvelope), input), false, nil
	case retiredEnvelope:
		if size != int64(len(retiredEnvelope)) {
			return nil, false, ErrCorruptPublication
		}
		return io.MultiReader(strings.NewReader(retiredEnvelope), input), true, nil
	default:
		return nil, false, ErrCorruptPublication
	}
}

func (f *Filesystem) walkObjects(ctx context.Context, yield blobObjectVisitor) (resultErr error) {
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := checkFilesystemActivation(ctx, f.root); err != nil {
		return err
	}
	for _, namespace := range []string{objectsDir, retainedDir, ".starport"} {
		if _, err := root.Lstat(namespace); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		err := fs.WalkDir(root.FS(), namespace, func(address string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			skip, err := backupControlEntry(address, info)
			if err != nil {
				return err
			}
			if skip {
				return nil
			}
			if !entry.Type().IsRegular() || !validBlobAddress(address) {
				return errors.New("blob: backup refuses an unexpected filesystem entry")
			}
			file, err := root.Open(address)
			if err != nil {
				return err
			}
			info, err = file.Stat()
			if err != nil {
				return errors.Join(err, file.Close())
			}
			if !info.Mode().IsRegular() {
				return errors.Join(errors.New("blob: backup requires regular files"), file.Close())
			}
			return errors.Join(yield(address, info.Size(), file), file.Close())
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (o *ObjectStore) walkObjects(ctx context.Context, legacy bool, yield blobObjectVisitor) error {
	paginator := s3.NewListObjectsV2Paginator(o.client, &s3.ListObjectsV2Input{Bucket: aws.String(o.bucket), Prefix: aws.String(o.listPrefix())})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, object := range page.Contents {
			name, ok := strings.CutPrefix(aws.ToString(object.Key), o.listPrefix())
			if !ok {
				return errors.New("blob: listing escaped the selected prefix")
			}
			if !legacy && (name == blobLayoutKey || name == blobActivationCurrent || name == blobImportKey) {
				continue
			}
			if name == blobImportKey {
				return ErrImportRestricted
			}
			address := name
			if legacy {
				address, err = legacyBlobAddress(name)
				if err != nil {
					return err
				}
			} else if !validBlobAddress(address) {
				return errors.New("blob: backup found an unsupported object address")
			}
			if aws.ToString(object.ETag) == "" {
				return errors.New("blob: listing omitted the object identity")
			}
			result, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.bucket), Key: object.Key, IfMatch: object.ETag})
			if err != nil {
				return err
			}
			if result.ContentLength == nil || object.Size == nil || *result.ContentLength != *object.Size {
				return errors.Join(errors.New("blob: object changed during listing"), result.Body.Close())
			}
			if err := errors.Join(yield(address, *result.ContentLength, result.Body), result.Body.Close()); err != nil {
				return err
			}
		}
	}
	return nil
}

func legacyBlobAddress(name string) (string, error) {
	if key, ok := strings.CutPrefix(name, retainedDir+"/"); ok {
		if err := ValidateKey(key); err != nil {
			return "", err
		}
		return blobAddress(retainedDir, key), nil
	}
	if err := ValidateKey(name); err != nil {
		return "", fmt.Errorf("blob: unknown legacy object: %w", err)
	}
	return blobAddress(objectsDir, name), nil
}
