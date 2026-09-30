package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

const adoptionOperation = "adoption"

// populatedStore returns an object store with retained domain objects. The
// activated kind completed an import, one replay, and activation before it
// accepted ordinary writes. Its check inspects that prior activation.
func populatedStore(t *testing.T, kind string) (*ObjectStore, func(context.Context) error) {
	t.Helper()
	if kind == "fresh" {
		store := snapshotObjects(t)
		seedPopulated(t, store)
		return store, nil
	}
	target, store, _, original := activationFixture(t, "objectstore")
	receipt, err := target.(ImportPublicationReplayer).ReplayPublication(t.Context(), "operation", original, publicationStep(), strings.NewReader("bytes"), snapshotDirectory(t))
	require.NoError(t, err)
	position, decision := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}, strings.Repeat("a", 64)
	require.NoError(t, target.(ImportReplayActivator).ActivateImportAt(t.Context(), "operation", original, position, decision))
	check := func(ctx context.Context) error {
		return target.(ImportActivationInspector).CheckActivatedImportAt(ctx, "operation", original, position, decision)
	}
	require.NoError(t, check(t.Context()))
	opened := reopenObjects(t, store.(*ObjectStore))
	seedPopulated(t, opened)
	return opened, check
}

func seedPopulated(t *testing.T, store *ObjectStore) {
	t.Helper()
	_, err := store.Put(t.Context(), "mutable", strings.NewReader("mutable bytes"))
	require.NoError(t, err)
	_, err = store.Publish(t.Context(), "live", strings.NewReader("published bytes"))
	require.NoError(t, err)
	require.NoError(t, store.Retire(t.Context(), "retired"))
}

// reopenObjects returns a new handle with no cached readiness for the same prefix.
func reopenObjects(t *testing.T, store *ObjectStore) *ObjectStore {
	t.Helper()
	reopened, err := NewObjectStore(t.Context(), ObjectStoreOptions{Endpoint: os.Getenv("TEST_BLOB_S3_ENDPOINT"), Region: "us-east-1", Bucket: store.bucket, Prefix: store.prefix, AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"})
	require.NoError(t, err)
	return reopened
}

func populatedTarget(t *testing.T, store *ObjectStore) PopulatedImportClaimer {
	t.Helper()
	target, err := ObjectRestoreTarget(store)
	require.NoError(t, err)
	return target.(PopulatedImportClaimer)
}

type populatedCapture struct {
	archive  string
	scratch  string
	snapshot Snapshot
	controls PopulatedControls
	domain   map[string]string
}

func capturePopulated(t *testing.T, store *ObjectStore) populatedCapture {
	t.Helper()
	archive := filepath.Join(snapshotDirectory(t), "captured.tar")
	snapshot, err := Backup(t.Context(), store, archive)
	require.NoError(t, err)
	controls, err := populatedTarget(t, store).ObservePopulatedControls(t.Context())
	require.NoError(t, err)
	return populatedCapture{archive: archive, scratch: snapshotDirectory(t), snapshot: snapshot, controls: controls, domain: domainDigests(t, store)}
}

func (c populatedCapture) claim(t *testing.T, store *ObjectStore, operation string) error {
	t.Helper()
	return populatedTarget(t, store).ClaimPopulated(t.Context(), c.archive, c.scratch, operation, c.snapshot, c.controls)
}

// objectListing records every version, delete marker, size, current digest, and
// retained metadata value under the prefix.
func objectListing(t *testing.T, store *ObjectStore) map[string]string {
	t.Helper()
	listing := map[string]string{}
	versions := s3.NewListObjectVersionsPaginator(store.client, &s3.ListObjectVersionsInput{Bucket: aws.String(store.bucket), Prefix: aws.String(store.listPrefix())})
	for versions.HasMorePages() {
		page, err := versions.NextPage(t.Context())
		require.NoError(t, err)
		for _, version := range page.Versions {
			listing["version "+aws.ToString(version.Key)+" "+aws.ToString(version.VersionId)] = fmt.Sprintf("%d %s %t", aws.ToInt64(version.Size), aws.ToString(version.ETag), aws.ToBool(version.IsLatest))
		}
		for _, marker := range page.DeleteMarkers {
			listing["marker "+aws.ToString(marker.Key)+" "+aws.ToString(marker.VersionId)] = strconv.FormatBool(aws.ToBool(marker.IsLatest))
		}
	}
	for key, digest := range currentDigests(t, store) {
		listing["current "+key] = digest
	}
	return listing
}

func currentDigests(t *testing.T, store *ObjectStore) map[string]string {
	t.Helper()
	digests := map[string]string{}
	objects := s3.NewListObjectsV2Paginator(store.client, &s3.ListObjectsV2Input{Bucket: aws.String(store.bucket), Prefix: aws.String(store.listPrefix())})
	for objects.HasMorePages() {
		page, err := objects.NextPage(t.Context())
		require.NoError(t, err)
		for _, object := range page.Contents {
			result, err := store.client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: object.Key})
			require.NoError(t, err)
			data, err := io.ReadAll(result.Body)
			require.NoError(t, err)
			require.NoError(t, result.Body.Close())
			digests[strings.TrimPrefix(aws.ToString(object.Key), store.listPrefix())] = fmt.Sprintf("%d %s %s", len(data), blobDigest(data), result.Metadata[retainedObjectMetadataKey])
		}
	}
	return digests
}

func domainDigests(t *testing.T, store *ObjectStore) map[string]string {
	t.Helper()
	digests := currentDigests(t, store)
	for key := range digests {
		if objectControlKey(key) {
			delete(digests, key)
		}
	}
	return digests
}

func readAll(t *testing.T, body io.ReadCloser, err error) string {
	t.Helper()
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	return string(data)
}

// checkPopulatedAdoption proves the final claim state and then runs import
// verification, one replay step, activation, and the activated-state check.
func checkPopulatedAdoption(t *testing.T, store *ObjectStore, capture populatedCapture) {
	t.Helper()
	ctx := t.Context()
	records := objectActivation{store: store}
	claim, err := makeBlobClaim(adoptionOperation, capture.snapshot)
	require.NoError(t, err)
	closure, err := makeAdoptionClosure(claim, capture.controls)
	require.NoError(t, err)
	for key, expected := range map[string][]byte{blobImportKey: claim, blobClosureKey: closure} {
		actual, err := records.read(ctx, key)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	for _, key := range []string{blobActivationCurrent, blobReplayCurrent} {
		_, err := records.read(ctx, key)
		require.ErrorIs(t, err, ErrNotFound)
	}
	require.Equal(t, capture.domain, domainDigests(t, store))
	require.ErrorIs(t, checkActivationBarrier(ctx, records), ErrImportRestricted)
	_, err = reopenObjects(t, store).Put(ctx, "blocked", strings.NewReader("blocked"))
	require.ErrorIs(t, err, ErrImportRestricted)

	target, err := ObjectRestoreTarget(store)
	require.NoError(t, err)
	require.NoError(t, target.(ImportInspector).CheckImport(ctx, adoptionOperation, capture.snapshot))
	recaptured, err := target.(ImportInspector).SnapshotImport(ctx, filepath.Join(snapshotDirectory(t), "adopted.tar"), adoptionOperation, capture.snapshot)
	require.NoError(t, err)
	require.Equal(t, capture.snapshot, recaptured, "capture excludes the closure receipt and keeps every domain byte")
	require.NoError(t, RestoreObjectStore(ctx, store, capture.archive, snapshotDirectory(t), adoptionOperation, capture.snapshot), "import verification skips the closure receipt")
	require.Error(t, RestoreObjectStore(ctx, store, capture.archive, snapshotDirectory(t), "other", capture.snapshot))

	step := ImportPublicationStep{Sequence: 1, EvidenceSHA256: strings.Repeat("e", 64), Key: "adopted-asset", Expected: PublicationState{Kind: "absent"}, Next: PublicationState{Kind: "live", Size: 7, SHA256: blobDigest([]byte("adopted"))}}
	receipt, err := target.(ImportPublicationReplayer).ReplayPublication(ctx, adoptionOperation, capture.snapshot, step, strings.NewReader("adopted"), snapshotDirectory(t))
	require.NoError(t, err)
	position, decision := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}, strings.Repeat("c", 64)
	require.NoError(t, target.(ImportReplayInspector).CheckImportPosition(ctx, adoptionOperation, capture.snapshot, position))
	require.NoError(t, target.(ImportReplayActivator).ActivateImportAt(ctx, adoptionOperation, capture.snapshot, position, decision))
	require.NoError(t, target.(ImportActivationInspector).CheckActivatedImportAt(ctx, adoptionOperation, capture.snapshot, position, decision))
	require.NoError(t, checkActivationBarrier(ctx, records))

	activated := domainDigests(t, store)
	for key, digest := range capture.domain {
		require.Equal(t, digest, activated[key], key)
	}
	opened := reopenObjects(t, store)
	body, err := opened.Get(ctx, "mutable")
	require.Equal(t, "mutable bytes", readAll(t, body, err))
	body, err = opened.ReadPublished(ctx, "live")
	require.Equal(t, "published bytes", readAll(t, body, err))
	body, err = opened.ReadPublished(ctx, "adopted-asset")
	require.Equal(t, "adopted", readAll(t, body, err))
	_, err = opened.ReadPublished(ctx, "retired")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = Backup(ctx, opened, filepath.Join(snapshotDirectory(t), "later.tar"))
	require.NoError(t, err)
}

func TestPopulatedClaimAdoptsActivatedStore(t *testing.T) {
	store, prior := populatedStore(t, "activated")
	capture := capturePopulated(t, store)
	require.True(t, capture.controls.Import.Present)
	require.True(t, capture.controls.ActivationCurrent.Present)
	require.True(t, capture.controls.ReplayCurrent.Present)
	require.False(t, capture.controls.Closure.Present)
	require.NoError(t, capture.claim(t, store, adoptionOperation))
	require.Error(t, prior(t.Context()))
	// The reply is lost. The retry uses a new handle and changes no object.
	before := objectListing(t, store)
	require.NoError(t, capture.claim(t, reopenObjects(t, store), adoptionOperation))
	require.Equal(t, before, objectListing(t, store))
	checkPopulatedAdoption(t, reopenObjects(t, store), capture)
}

func TestPopulatedClaimAdoptsStoreWithoutActivation(t *testing.T) {
	store, _ := populatedStore(t, "fresh")
	capture := capturePopulated(t, store)
	require.Equal(t, PopulatedControls{}, capture.controls)
	require.NoError(t, capture.claim(t, store, adoptionOperation))
	before := objectListing(t, store)
	require.NoError(t, capture.claim(t, reopenObjects(t, store), adoptionOperation))
	require.Equal(t, before, objectListing(t, store))
	checkPopulatedAdoption(t, reopenObjects(t, store), capture)
}

type interruptedPopulated struct {
	populatedRecords
	after, calls int
}

var errPopulatedReplyLost = errors.New("populated claim reply lost")

func (f *interruptedPopulated) lost(err error) error {
	if err != nil {
		return err
	}
	f.calls++
	if f.calls == f.after {
		return errPopulatedReplyLost
	}
	return nil
}

func (f *interruptedPopulated) replace(ctx context.Context, name string, prior PopulatedControl, value []byte) error {
	return f.lost(f.populatedRecords.replace(ctx, name, prior, value))
}

func (f *interruptedPopulated) retire(ctx context.Context, name string, prior PopulatedControl) error {
	return f.lost(f.populatedRecords.retire(ctx, name, prior))
}

func TestPopulatedClaimResumesEveryDurableStep(t *testing.T) {
	for _, kind := range []string{"activated", "fresh"} {
		t.Run(kind, func(t *testing.T) {
			// The steps are the closure, the claim, the replay cursor, and the activation root.
			for after := 1; after <= 4; after++ {
				t.Run(strconv.Itoa(after), func(t *testing.T) {
					ctx := t.Context()
					store, prior := populatedStore(t, kind)
					capture := capturePopulated(t, store)
					interrupted := &interruptedPopulated{populatedRecords: objectActivation{store: store}, after: after}
					require.ErrorIs(t, claimPopulated(ctx, store, interrupted, capture.archive, capture.scratch, adoptionOperation, capture.snapshot, capture.controls), errPopulatedReplyLost)

					opened := reopenObjects(t, store)
					require.ErrorIs(t, checkActivationBarrier(ctx, objectActivation{store: opened}), ErrImportRestricted)
					_, err := opened.Put(ctx, "blocked", strings.NewReader("blocked"))
					require.ErrorIs(t, err, ErrImportRestricted)
					_, err = Backup(ctx, opened, filepath.Join(snapshotDirectory(t), "blocked.tar"))
					require.ErrorIs(t, err, ErrImportRestricted)
					if prior != nil {
						require.Error(t, prior(ctx))
					}
					target, err := ObjectRestoreTarget(opened)
					require.NoError(t, err)
					// A store without activation has no root to retire, so the claim is its final write.
					if after == 1 || prior != nil && after < 4 {
						require.Error(t, target.(ImportInspector).CheckImport(ctx, adoptionOperation, capture.snapshot))
					}
					_, err = populatedTarget(t, opened).ObservePopulatedControls(ctx)
					require.ErrorIs(t, err, ErrActivationConflict)
					before := objectListing(t, opened)
					require.ErrorIs(t, capture.claim(t, opened, "other"), ErrActivationConflict)
					require.Equal(t, before, objectListing(t, opened))

					require.NoError(t, capture.claim(t, opened, adoptionOperation))
					require.NoError(t, target.(ImportInspector).CheckImport(ctx, adoptionOperation, capture.snapshot))
					require.Equal(t, capture.domain, domainDigests(t, opened))
				})
			}
		})
	}
}

func TestPopulatedClaimRefusalsLeaveNoChange(t *testing.T) {
	for _, refusal := range []string{"changed-object", "extra-object", "missing-object", "changed-retirement", "changed-activation-current", "different-claim"} {
		t.Run(refusal, func(t *testing.T) {
			ctx := t.Context()
			store, _ := populatedStore(t, "activated")
			capture := capturePopulated(t, store)
			opened := reopenObjects(t, store)
			expected := ErrPopulatedContentsChanged
			var err error
			switch refusal {
			case "changed-object":
				_, err = opened.Put(ctx, "mutable", strings.NewReader("changed bytes"))
			case "extra-object":
				_, err = opened.Put(ctx, "extra", strings.NewReader("extra bytes"))
			case "missing-object":
				err = opened.Delete(ctx, "mutable")
			case "changed-retirement":
				err = opened.Retire(ctx, "live")
			case "changed-activation-current":
				expected = ErrActivationConflict
				_, err = opened.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(opened.bucket), Key: aws.String(opened.objectKey(blobActivationCurrent)), Body: strings.NewReader("changed")})
			case "different-claim":
				expected = ErrActivationConflict
				claim, claimErr := makeBlobClaim("other", capture.snapshot)
				require.NoError(t, claimErr)
				_, err = opened.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(opened.bucket), Key: aws.String(opened.objectKey(blobImportKey)), Body: bytes.NewReader(claim)})
			}
			require.NoError(t, err)
			before := objectListing(t, store)
			require.ErrorIs(t, capture.claim(t, reopenObjects(t, store), adoptionOperation), expected)
			require.Equal(t, before, objectListing(t, store))
			if expected == ErrActivationConflict {
				_, err = populatedTarget(t, reopenObjects(t, store)).ObservePopulatedControls(ctx)
				require.ErrorIs(t, err, ErrActivationConflict)
			}
		})
	}
}

func TestPopulatedClaimRefusesPendingImport(t *testing.T) {
	ctx := t.Context()
	target, store, archive, snapshot := activationFixture(t, "objectstore")
	objects := store.(*ObjectStore)
	_, err := target.(PopulatedImportClaimer).ObservePopulatedControls(ctx)
	require.ErrorIs(t, err, ErrActivationConflict)
	claim, err := objectActivation{store: objects}.observe(ctx, blobImportKey)
	require.NoError(t, err)
	before := objectListing(t, objects)
	controls := PopulatedControls{Import: claim}
	require.ErrorIs(t, target.(PopulatedImportClaimer).ClaimPopulated(ctx, archive, snapshotDirectory(t), adoptionOperation, snapshot, controls), ErrActivationConflict)
	require.Equal(t, before, objectListing(t, objects))
}

func TestPopulatedClaimKeepsOrdinaryImportRefusal(t *testing.T) {
	store, _ := populatedStore(t, "fresh")
	capture := capturePopulated(t, store)
	before := objectListing(t, store)
	require.ErrorContains(t, RestoreObjectStore(t.Context(), store, capture.archive, snapshotDirectory(t), adoptionOperation, capture.snapshot), "import target contains existing objects")
	require.Equal(t, before, objectListing(t, store))
}

func filesystemListing(t *testing.T, root string) map[string]string {
	t.Helper()
	listing := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			listing[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- the test reads its own temporary tree.
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		listing[path] = hex.EncodeToString(sum[:])
		return nil
	}))
	return listing
}

func TestPopulatedClaimRefusesFilesystem(t *testing.T) {
	target, store, archive, snapshot := activationFixture(t, "filesystem")
	root := store.(*Filesystem).root
	before := filesystemListing(t, root)
	claimer, ok := target.(PopulatedImportClaimer)
	require.True(t, ok)
	_, err := claimer.ObservePopulatedControls(t.Context())
	require.ErrorIs(t, err, ErrPopulatedClaimUnsupported)
	require.ErrorIs(t, claimer.ClaimPopulated(t.Context(), archive, snapshotDirectory(t), adoptionOperation, snapshot, PopulatedControls{}), ErrPopulatedClaimUnsupported)
	require.Equal(t, before, filesystemListing(t, root))
}
