package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// blobClosureKey binds a populated claim to the observed control preimage.
// Activation history for its claim consumes it.
const blobClosureKey = ".starport/adoption-closure"

// ErrPopulatedClaimUnsupported refuses a populated claim outside object storage.
var ErrPopulatedClaimUnsupported = errors.New("blob: populated claim requires object storage")

// ErrPopulatedContentsChanged refuses retained objects that differ from the captured snapshot.
var ErrPopulatedContentsChanged = errors.New("blob: retained objects differ from the captured snapshot")

// PopulatedControl records the exact bytes and ETag of one control object.
// Present is false when the object does not exist.
type PopulatedControl struct {
	Present bool   `json:"present"`
	Value   []byte `json:"value,omitempty"`
	ETag    string `json:"etag,omitempty"`
}

// PopulatedControls is the control preimage that the caller keeps from preparation.
// A captured snapshot excludes these objects.
type PopulatedControls struct {
	Import            PopulatedControl `json:"import"`
	ActivationCurrent PopulatedControl `json:"activation_current"`
	ReplayCurrent     PopulatedControl `json:"replay_current"`
	Closure           PopulatedControl `json:"closure"`
}

// PopulatedImportClaimer puts an import claim on retained bytes in place.
// The coordinator must fence all target writers before it observes the controls
// and must keep them fenced through activation. A failed claim can leave durable
// restricted work. A retry with the same inputs continues it.
type PopulatedImportClaimer interface {
	ObservePopulatedControls(context.Context) (PopulatedControls, error)
	ClaimPopulated(context.Context, string, string, string, Snapshot, PopulatedControls) error
}

type blobAdoptionClosure struct {
	Version        int    `json:"version"`
	ClaimSHA256    string `json:"claim_sha256"`
	ControlsSHA256 string `json:"controls_sha256"`
}

// populatedRecords owns the conditional control changes of a populated claim.
// replace and retire refuse a current value that differs from the prior value.
type populatedRecords interface {
	activationRecords
	observe(context.Context, string) (PopulatedControl, error)
	replace(context.Context, string, PopulatedControl, []byte) error
	retire(context.Context, string, PopulatedControl) error
}

func (filesystemRestoreTarget) ObservePopulatedControls(context.Context) (PopulatedControls, error) {
	return PopulatedControls{}, ErrPopulatedClaimUnsupported
}

func (filesystemRestoreTarget) ClaimPopulated(context.Context, string, string, string, Snapshot, PopulatedControls) error {
	return ErrPopulatedClaimUnsupported
}

// ObservePopulatedControls reads the control preimage without a change.
// It refuses a pending import, a pending activation, and a pending closure.
func (t objectRestoreTarget) ObservePopulatedControls(ctx context.Context) (PopulatedControls, error) {
	if t.store == nil {
		return PopulatedControls{}, ErrPopulatedClaimUnsupported
	}
	return observePopulatedControls(ctx, t.store, objectActivation(t))
}

// ClaimPopulated verifies retained objects against the captured snapshot and then
// puts the import claim. It changes no object before the closure receipt.
func (t objectRestoreTarget) ClaimPopulated(ctx context.Context, source, scratch, operation string, captured Snapshot, controls PopulatedControls) error {
	if t.store == nil {
		return ErrPopulatedClaimUnsupported
	}
	return claimPopulated(ctx, t.store, objectActivation(t), source, scratch, operation, captured, controls)
}

func observePopulatedControls(ctx context.Context, store *ObjectStore, records populatedRecords) (PopulatedControls, error) {
	if err := ctx.Err(); err != nil {
		return PopulatedControls{}, err
	}
	var controls PopulatedControls
	for name, control := range controls.fields() {
		observed, err := records.observe(ctx, name)
		if err != nil {
			return PopulatedControls{}, err
		}
		*control = observed
	}
	if err := checkPopulatedControls(ctx, records, controls); err != nil {
		return PopulatedControls{}, err
	}
	if err := store.checkPopulatedLayout(ctx); err != nil {
		return PopulatedControls{}, err
	}
	return controls, nil
}

// checkPopulatedLayout requires the portable layout. A legacy prefix needs migration.
func (o *ObjectStore) checkPopulatedLayout(ctx context.Context) error {
	err := o.readLayout(ctx)
	if isAbsent(err) {
		return ErrLayoutMigrationRequired
	}
	return err
}

func (c *PopulatedControls) fields() map[string]*PopulatedControl {
	return map[string]*PopulatedControl{blobImportKey: &c.Import, blobActivationCurrent: &c.ActivationCurrent, blobReplayCurrent: &c.ReplayCurrent, blobClosureKey: &c.Closure}
}

// checkPopulatedControls accepts the preimage of an ordinary store or of a completed activation.
func checkPopulatedControls(ctx context.Context, records activationRecords, controls PopulatedControls) error {
	for _, control := range controls.fields() {
		if control.Present == (control.ETag == "") || !control.Present && len(control.Value) != 0 {
			return ErrActivationConflict
		}
	}
	if !controls.Import.Present {
		if controls.ActivationCurrent.Present || controls.ReplayCurrent.Present {
			return ErrActivationConflict
		}
	} else {
		receipt, ok := activeReceipt(controls.Import.Value)
		if !ok || !controls.ActivationCurrent.Present || !bytes.Equal(controls.ActivationCurrent.Value, controls.Import.Value) {
			return ErrActivationConflict
		}
		receipt.Phase = ""
		history, _ := json.Marshal(receipt)
		recorded, err := records.read(ctx, blobActivationPrefix+receipt.ClaimSHA256)
		if err != nil || !bytes.Equal(recorded, history) {
			return errors.Join(ErrActivationConflict, err)
		}
		if controls.ReplayCurrent.Present {
			var cursor blobReplayCursor
			if json.Unmarshal(controls.ReplayCurrent.Value, &cursor, json.RejectUnknownMembers(true)) != nil || cursor.Phase != replayComplete ||
				!cursor.Receipt.valid() || cursor.Receipt.ClaimSHA256 != receipt.ClaimSHA256 {
				return ErrActivationConflict
			}
			canonical, _ := json.Marshal(cursor)
			if !bytes.Equal(canonical, controls.ReplayCurrent.Value) {
				return ErrActivationConflict
			}
		}
	}
	if controls.Closure.Present {
		if err := checkClosureConsumed(ctx, records, controls.Closure.Value); err != nil {
			return errors.Join(ErrActivationConflict, err)
		}
	}
	return ctx.Err()
}

// checkAdoptionClosure refuses a populated claim that has not reached activation.
func checkAdoptionClosure(ctx context.Context, records activationRecords) error {
	value, err := records.read(ctx, blobClosureKey)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return errors.Join(ErrImportRestricted, err)
	}
	return checkClosureConsumed(ctx, records, value)
}

func checkClosureConsumed(ctx context.Context, records activationRecords, value []byte) error {
	var closure blobAdoptionClosure
	if json.Unmarshal(value, &closure, json.RejectUnknownMembers(true)) != nil || closure.Version != 1 ||
		!activationDigest(closure.ClaimSHA256) || !activationDigest(closure.ControlsSHA256) {
		return ErrImportRestricted
	}
	canonical, _ := json.Marshal(closure)
	if !bytes.Equal(canonical, value) {
		return ErrImportRestricted
	}
	if _, err := records.read(ctx, blobActivationPrefix+closure.ClaimSHA256); err != nil {
		return errors.Join(ErrImportRestricted, err)
	}
	return nil
}

func makeAdoptionClosure(claim []byte, controls PopulatedControls) ([]byte, error) {
	body, err := json.Marshal(controls)
	if err != nil {
		return nil, err
	}
	return json.Marshal(blobAdoptionClosure{Version: 1, ClaimSHA256: blobDigest(claim), ControlsSHA256: blobDigest(body)})
}

func samePopulatedControl(a, b PopulatedControl) bool {
	return a.Present == b.Present && a.ETag == b.ETag && bytes.Equal(a.Value, b.Value)
}

func claimPopulated(ctx context.Context, store *ObjectStore, records populatedRecords, source, scratch, operation string, captured Snapshot, controls PopulatedControls) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, captured)
	if err != nil {
		return err
	}
	closure, err := makeAdoptionClosure(claim, controls)
	if err != nil {
		return err
	}
	image, err := prepareBlobImage(ctx, scratch, source, captured)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.close()) }()
	identities, err := store.verifyPopulatedContents(ctx, image, captured)
	if err != nil {
		return err
	}
	// An equal closure continues earlier work. Its digest binds the same preimage.
	current, err := records.observe(ctx, blobClosureKey)
	if err != nil {
		return err
	}
	if !current.Present || !bytes.Equal(current.Value, closure) {
		if err := checkPopulatedPreimage(ctx, store, records, claim, controls); err != nil {
			return err
		}
		if err := records.replace(ctx, blobClosureKey, controls.Closure, closure); err != nil {
			return err
		}
	}
	if err := records.replace(ctx, blobImportKey, controls.Import, claim); err != nil {
		return err
	}
	// The stale cursor binds the prior claim and refuses every replay for this claim.
	if err := records.retire(ctx, blobReplayCurrent, controls.ReplayCurrent); err != nil {
		return err
	}
	if err := records.retire(ctx, blobActivationCurrent, controls.ActivationCurrent); err != nil {
		return err
	}
	return store.checkPopulatedClaim(ctx, records, claim, closure, identities)
}

func checkPopulatedPreimage(ctx context.Context, store *ObjectStore, records populatedRecords, claim []byte, controls PopulatedControls) error {
	if err := checkPopulatedControls(ctx, records, controls); err != nil {
		return err
	}
	if err := store.checkPopulatedLayout(ctx); err != nil {
		return err
	}
	for name, expected := range controls.fields() {
		actual, err := records.observe(ctx, name)
		if err != nil {
			return err
		}
		if !samePopulatedControl(actual, *expected) {
			return ErrActivationConflict
		}
	}
	if _, err := records.read(ctx, blobActivationPrefix+blobDigest(claim)); !errors.Is(err, ErrNotFound) {
		return errors.Join(ErrActivationConflict, err)
	}
	return ctx.Err()
}

func (o *ObjectStore) checkPopulatedClaim(ctx context.Context, records activationRecords, claim, closure []byte, identities map[string]string) error {
	for name, expected := range map[string][]byte{blobClosureKey: closure, blobImportKey: claim} {
		actual, err := records.read(ctx, name)
		if err != nil || !bytes.Equal(actual, expected) {
			return errors.Join(ErrActivationConflict, err)
		}
	}
	for _, name := range []string{blobActivationCurrent, blobReplayCurrent, blobActivationPrefix + blobDigest(claim)} {
		if _, err := records.read(ctx, name); !errors.Is(err, ErrNotFound) {
			return errors.Join(ErrActivationConflict, err)
		}
	}
	listed := 0
	err := o.listPopulated(ctx, func(name string, object s3types.Object) error {
		if identities[name] == "" || identities[name] != aws.ToString(object.ETag) {
			return ErrPopulatedContentsChanged
		}
		listed++
		return nil
	})
	if err != nil {
		return err
	}
	if listed != len(identities) {
		return ErrPopulatedContentsChanged
	}
	return nil
}

// verifyPopulatedContents compares every retained domain object with the image.
// It returns the listed ETag of each object so a later listing can detect change.
func (o *ObjectStore) verifyPopulatedContents(ctx context.Context, image *blobImage, expected Snapshot) (map[string]string, error) {
	identities := map[string]string{}
	var retired int64
	err := o.listPopulated(ctx, func(name string, object s3types.Object) error {
		if !validBlobAddress(name) || aws.ToString(object.ETag) == "" {
			return ErrPopulatedContentsChanged
		}
		info, err := image.root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || object.Size == nil || *object.Size != info.Size() {
			return errors.Join(ErrPopulatedContentsChanged, err)
		}
		wasRetired, err := o.verifyPopulatedObject(ctx, image, name, object)
		if err != nil {
			return err
		}
		if wasRetired {
			retired++
		}
		identities[name] = aws.ToString(object.ETag)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if int64(len(identities)) != expected.Objects || retired != expected.Retired {
		return nil, ErrPopulatedContentsChanged
	}
	return identities, nil
}

func (o *ObjectStore) verifyPopulatedObject(ctx context.Context, image *blobImage, name string, object s3types.Object) (retired bool, resultErr error) {
	result, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.bucket), Key: object.Key, IfMatch: object.ETag})
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, result.Body.Close()) }()
	size := aws.ToInt64(object.Size)
	if result.ContentLength == nil || *result.ContentLength != size {
		return false, ErrPopulatedContentsChanged
	}
	reader, retired, err := inspectBlobEnvelope(name, size, result.Body)
	if err != nil {
		return false, errors.Join(ErrPopulatedContentsChanged, err)
	}
	metadata := ""
	if strings.HasPrefix(name, retainedDir+"/") {
		metadata = liveObjectMetadata
		if retired {
			metadata = retiredObjectMetadata
		}
	}
	if result.Metadata[retainedObjectMetadataKey] != metadata {
		return false, ErrPopulatedContentsChanged
	}
	hash := sha256.New()
	n, err := io.Copy(hash, &contextReader{ctx: ctx, r: io.LimitReader(reader, size+1)})
	if err != nil {
		return false, err
	}
	original, err := hashRestoreFile(ctx, image.root, name, size)
	if err != nil {
		return false, errors.Join(ErrPopulatedContentsChanged, err)
	}
	if n != size || !bytes.Equal(hash.Sum(nil), original[:]) {
		return false, ErrPopulatedContentsChanged
	}
	return retired, nil
}

// listPopulated visits every listed object except control objects.
func (o *ObjectStore) listPopulated(ctx context.Context, visit func(string, s3types.Object) error) error {
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
			if objectControlKey(name) {
				continue
			}
			if err := visit(name, object); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o objectActivation) observe(ctx context.Context, name string) (PopulatedControl, error) {
	value, tag, err := o.get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return PopulatedControl{}, nil
	}
	if err != nil {
		return PopulatedControl{}, err
	}
	return PopulatedControl{Present: true, Value: value, ETag: tag}, nil
}

// replace writes value only over the exact prior bytes and ETag.
func (o objectActivation) replace(ctx context.Context, name string, prior PopulatedControl, value []byte) error {
	current, err := o.observe(ctx, name)
	if err != nil {
		return err
	}
	if current.Present && bytes.Equal(current.Value, value) {
		return nil
	}
	if !samePopulatedControl(current, prior) {
		return ErrActivationConflict
	}
	input := &s3.PutObjectInput{Bucket: aws.String(o.store.bucket), Key: aws.String(o.store.objectKey(name)), Body: bytes.NewReader(value)}
	if prior.Present {
		input.IfMatch = aws.String(prior.ETag)
	} else {
		input.IfNoneMatch = aws.String("*")
	}
	_, err = o.store.client.PutObject(ctx, input)
	if isPublicationConflict(err) {
		current, readErr := o.read(ctx, name)
		if readErr == nil && bytes.Equal(current, value) {
			return nil
		}
		return errors.Join(ErrActivationConflict, readErr)
	}
	return err
}

// retire deletes only the exact prior bytes and ETag. The delete carries If-Match.
// A service that ignores If-Match on deletion depends on the fenced writers and on
// the claim barrier, which permits no other writer of these keys.
func (o objectActivation) retire(ctx context.Context, name string, prior PopulatedControl) error {
	current, err := o.observe(ctx, name)
	if err != nil || !current.Present {
		return err
	}
	if !prior.Present || !samePopulatedControl(current, prior) {
		return ErrActivationConflict
	}
	_, err = o.store.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(o.store.bucket), Key: aws.String(o.store.objectKey(name)), IfMatch: aws.String(prior.ETag)})
	if err != nil && !isPublicationConflict(err) {
		return err
	}
	current, err = o.observe(ctx, name)
	if err != nil {
		return err
	}
	if current.Present {
		return ErrActivationConflict
	}
	return nil
}

var (
	_ PopulatedImportClaimer = filesystemRestoreTarget{}
	_ PopulatedImportClaimer = objectRestoreTarget{}
)
