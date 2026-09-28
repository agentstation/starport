package recovery

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBundleReferencesRequireMoreThanArtifactDigestsAndKeyChallenge(t *testing.T) {
	for _, mode := range []string{"valid", "different-historical-key", "missing-file-bytes", "missing-job-bytes"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, records, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = records
			key := source.Encryption
			if mode == "different-historical-key" {
				var err error
				key, err = credentials.NewEncryptionService([]byte(strings.Repeat("z", 32)))
				require.NoError(t, err)
			}
			secret, err := key.EncryptCredential(`{"api_key":"private-test-credential"}`)
			require.NoError(t, err)
			keys, err := credentials.Open(kv)
			require.NoError(t, err)
			_, err = keys.Create(t.Context(), credentials.ProviderKey{Scope: "owner", Provider: "former-provider", EncryptedCredential: secret})
			require.NoError(t, err)
			fileRecords, err := files.OpenRepository(kv)
			require.NoError(t, err)
			fileService, err := files.NewService(fileRecords, source.Blobs)
			require.NoError(t, err)
			inputFile, err := fileService.Upload(t.Context(), files.UploadRequest{Account: "owner", Filename: "input.txt", Purpose: files.PurposeBatch, Size: 5}, strings.NewReader("bytes"))
			require.NoError(t, err)
			batch, err := jobs.NewBatch("captured-batch", "owner", "/v1/chat/completions", inputFile.ID, time.Now())
			require.NoError(t, err)
			batch.State, batch.TotalLines = jobs.JobStateRunning, 1
			batches, err := jobs.OpenBatchRepository(kv)
			require.NoError(t, err)
			require.NoError(t, batches.Create(t.Context(), batch))
			_, err = batches.ClaimLine(t.Context(), batch.Account, batch.ID, 1, strings.Repeat("a", 64))
			require.NoError(t, err)
			job, err := jobs.New("one", "owner", "provider", "model", routing.OperationVideosGenerations, time.Now().Add(-time.Minute))
			require.NoError(t, err)
			require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
			require.NoError(t, job.StoreAsset("job-asset", "video/mp4", 5, time.Now().Add(time.Hour)))
			jobRecords, err := jobs.OpenRepository(kv)
			require.NoError(t, err)
			require.NoError(t, jobRecords.Create(t.Context(), job))
			if mode != "missing-job-bytes" {
				_, err = source.Blobs.Publish(t.Context(), "job-asset", strings.NewReader("video"))
				require.NoError(t, err)
			}
			if mode == "missing-file-bytes" {
				source.Blobs, err = blob.NewFilesystem(filepath.Join(privateKVDirectory(t), "unmatched"))
				require.NoError(t, err)
			}
			manifest, err := BackupBundle(t.Context(), destination, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			// Byte integrity and access to the selected key alone cannot prove references.
			_, err = VerifyBundle(t.Context(), destination, digest, source.Encryption)
			require.NoError(t, err)
			_, report, err := InspectBundleReferences(t.Context(), destination, digest, privateKVDirectory(t), source.Encryption)
			if mode == "valid" {
				require.NoError(t, err)
				require.Equal(t, ReferenceReport{CredentialRecords: 1, CredentialValues: 1, FileRecords: 1, JobRecords: 1, BatchRecords: 1, BatchLines: 1, UnfinishedBatchLines: 1}, report)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-test-credential")
			}
		})
	}
}

func TestKVSnapshotViewBoundsReadsAndRemovesOnlyItsCopy(t *testing.T) {
	path, receipt := kvSnapshotFixture(t)
	scratch := privateKVDirectory(t)
	view, err := OpenKVSnapshot(t.Context(), path, scratch, receipt)
	require.NoError(t, err)
	value, err := view.GetBounded(t.Context(), "account:one", 1024)
	require.NoError(t, err)
	require.Equal(t, "encrypted-credentials", string(value))
	_, err = view.GetBounded(t.Context(), "account:one", 2)
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
	_, err = view.GetBounded(t.Context(), "missing", 2)
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, view.Close())
	require.FileExists(t, path)
}

func TestBundleReferencesRejectInvalidSQLIdentity(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	_, err := source.SQL.ExecContext(t.Context(), "INSERT INTO users(id,subject,revision,record) VALUES('user','subject',1,'{}')")
	require.NoError(t, err)
	manifest, err := BackupBundle(t.Context(), destination, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	_, _, err = InspectBundleReferences(t.Context(), destination, digest, privateKVDirectory(t), source.Encryption)
	require.Error(t, err, "valid SQL bytes do not establish a valid identity record")
}

func TestBundleIdentityReferencesPreserveGrantsAndRefuseCorruption(t *testing.T) {
	for _, mode := range []string{"valid", "large-template", "deleted-account", "user-revision", "user-subject", "team-revision", "membership-user", "grant-team", "template-revision", "oversized-user"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, records, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = records
			accounts, err := account.Open(kv)
			require.NoError(t, err)
			owner, err := accounts.Create(t.Context(), account.Account{ID: "owner", Name: "Owner", Active: true, CredentialStrategy: account.StrategyOperatorFirst})
			require.NoError(t, err)
			people, err := identity.Open(source.SQL)
			require.NoError(t, err)
			_, err = people.Users.Create(t.Context(), identity.User{ID: "user", Subject: "provider:subject"})
			require.NoError(t, err)
			_, err = people.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
			require.NoError(t, err)
			_, err = people.Memberships.Add(t.Context(), identity.Membership{UserID: "user", TeamID: "team"})
			require.NoError(t, err)
			_, err = people.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "owner", TeamID: "team"})
			require.NoError(t, err)
			templates, err := account.OpenTemplates(source.SQL)
			require.NoError(t, err)
			template := account.Template{ID: "template", Name: "Template"}
			if mode == "large-template" {
				template.Access = []account.ProviderAccess{{Provider: strings.Repeat("p", policyrecord.MaxBytes+1)}}
			}
			_, err = templates.Create(t.Context(), template)
			require.NoError(t, err)
			query := map[string]string{
				"user-revision":     "UPDATE users SET revision=2",
				"user-subject":      "UPDATE users SET subject='other-subject'",
				"team-revision":     "UPDATE teams SET revision=2",
				"membership-user":   "UPDATE team_memberships SET user_id='unknown'",
				"grant-team":        "UPDATE account_grants SET team_id='unknown'",
				"template-revision": "UPDATE account_templates SET revision=2",
			}[mode]
			if query != "" {
				_, err = source.SQL.ExecContext(t.Context(), query)
				require.NoError(t, err)
			}
			if mode == "oversized-user" {
				_, err = source.SQL.ExecContext(t.Context(), "UPDATE users SET record=?", strings.Repeat("x", policyrecord.MaxBytes+1))
				require.NoError(t, err)
			}
			if mode == "deleted-account" {
				require.NoError(t, accounts.Delete(t.Context(), "owner", owner.Revision))
			}
			manifest, err := BackupBundle(t.Context(), destination, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			_, report, err := InspectBundleReferences(t.Context(), destination, digest, privateKVDirectory(t), source.Encryption)
			if mode != "valid" && mode != "large-template" && mode != "deleted-account" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, report.AccountTemplates)
			require.Equal(t, identity.RecoveryReport{Users: 1, Teams: 1, Memberships: 1, Grants: 1, MissingGrantAccounts: map[bool]int64{true: 1}[mode == "deleted-account"]}, report.Identity)
			if mode == "deleted-account" {
				require.Zero(t, report.AccountRecords)
			} else {
				require.EqualValues(t, 1, report.AccountRecords)
			}
		})
	}
}
