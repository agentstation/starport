package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/deployment"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

const freshHistoryMaximumRecords = 10000
const freshHistoryMaximumBytes = 128 << 20

func freshHistoryPrivateDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root, err = os.MkdirTemp(root, "private-census-")
	require.NoError(t, err)
	return root
}

type freshHistoryKVRecord struct {
	SHA256  string
	Size    int
	Expires int64
}

type freshHistoryCensus struct {
	KV      map[string]freshHistoryKVRecord
	SQL     map[string]string
	Objects map[string]string
	Files   map[string]string
}

func censusFreshHistory(t *testing.T, cfg *config.Config, request RecoveryActivationRequest) freshHistoryCensus {
	t.Helper()
	return freshHistoryCensus{
		KV:      censusFreshHistoryKV(t, cfg.RuntimeStorage().Valkey),
		SQL:     censusFreshHistorySQL(t, cfg.Storage.RuntimeSQL()),
		Objects: censusFreshHistoryObjects(t, cfg.Files.ObjectStore),
		Files:   censusFreshHistoryFiles(t, cfg, request),
	}
}

// The raw census includes private native controls while imports remain closed.
// Exact expiry comes from the same native read as the bounded record bytes.
func censusFreshHistoryKV(t *testing.T, settings storage.ValkeyConfig) map[string]freshHistoryKVRecord {
	t.Helper()
	store, err := storage.OpenValkey(settings)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	keys, err := store.ScanWithPrefix(t.Context(), "", freshHistoryMaximumRecords+1)
	require.NoError(t, err)
	require.Less(t, len(keys), freshHistoryMaximumRecords+1, "native census must reach the final cursor")
	options, err := valkey.ParseURL(settings.URL)
	require.NoError(t, err)
	options.DisableCache, options.ForceSingleClient = true, true
	if settings.DB != 0 {
		options.SelectDB = settings.DB
	}
	client, err := valkey.NewClient(options)
	require.NoError(t, err)
	defer client.Close()
	prefix, err := deployment.KeyPrefix(settings.DeploymentID)
	require.NoError(t, err)
	prefix = "{" + prefix + "}kv:"
	const read = `if redis.call('EXISTS', KEYS[1]) == 0 then return false end
if redis.call('STRLEN', KEYS[1]) > tonumber(ARGV[1]) then return redis.error_reply('CENSUS_VALUE_TOO_LARGE') end
return {redis.call('GET', KEYS[1]), redis.call('PEXPIRETIME', KEYS[1])}`
	result := make(map[string]freshHistoryKVRecord, len(keys))
	total := 0
	for _, key := range keys {
		values, err := client.Do(t.Context(), client.B().Eval().Script(read).Numkeys(1).Key(prefix+key).Arg(strconv.Itoa(freshHistoryMaximumBytes-total)).Build()).ToArray()
		require.NoError(t, err, "retained fixture records must remain present during census")
		require.Len(t, values, 2)
		body, err := values[0].AsBytes()
		require.NoError(t, err)
		total += len(body)
		require.LessOrEqual(t, total, freshHistoryMaximumBytes)
		expires, err := values[1].AsInt64()
		require.NoError(t, err)
		require.GreaterOrEqual(t, expires, int64(-1))
		result[key] = freshHistoryKVRecord{canonicalRecordSHA256(body), len(body), expires}
	}
	return result
}

// The portable export validates the complete relational schema and audit counter.
// Logical rows avoid treating SQLite page layout as a change in source state.
func censusFreshHistorySQL(t *testing.T, settings sqlstore.Config) map[string]string {
	t.Helper()
	db, err := sqlstore.Open(settings)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	root := freshHistoryPrivateDirectory(t)
	image := filepath.Join(root, "image")
	captured, err := db.SnapshotRelational(t.Context(), image)
	require.NoError(t, err)
	require.True(t, captured.Published)
	view, err := sqlstore.OpenRelationalSnapshot(t.Context(), filepath.Join(image, "starport.db"), captured.Snapshot, root)
	require.NoError(t, err)
	defer func() { require.NoError(t, view.Close()) }()
	objects, err := view.QueryContext(t.Context(), "SELECT name,type,sql FROM sqlite_schema ORDER BY name")
	require.NoError(t, err)
	result := map[string]string{}
	var tables []string
	for objects.Next() {
		var name, kind string
		var definition *string
		require.NoError(t, objects.Scan(&name, &kind, &definition))
		body, err := json.Marshal([]any{kind, definition})
		require.NoError(t, err)
		result["schema/"+name] = canonicalRecordSHA256(body)
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	require.NoError(t, objects.Err())
	require.NoError(t, objects.Close())
	total, count := 0, 0
	for _, table := range tables {
		rows, err := view.QueryContext(t.Context(), "SELECT * FROM \""+strings.ReplaceAll(table, "\"", "\"\"")+"\"")
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		var encodedRows []string
		for rows.Next() {
			values, destinations := make([]any, len(columns)), make([]any, len(columns))
			for index := range values {
				destinations[index] = &values[index]
			}
			require.NoError(t, rows.Scan(destinations...))
			body, err := json.Marshal(values, json.Deterministic(true))
			require.NoError(t, err)
			total += len(body)
			count++
			require.LessOrEqual(t, total, freshHistoryMaximumBytes)
			require.LessOrEqual(t, count, freshHistoryMaximumRecords)
			encodedRows = append(encodedRows, string(body))
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		slices.Sort(encodedRows)
		body, err := json.Marshal(struct{ Columns, Rows []string }{columns, encodedRows}, json.Deterministic(true))
		require.NoError(t, err)
		result["rows/"+table] = canonicalRecordSHA256(body)
	}
	return result
}

func censusFreshHistoryObjects(t *testing.T, settings config.ObjectStoreConfig) map[string]string {
	t.Helper()
	client := s3.NewFromConfig(aws.Config{Region: settings.Region, Credentials: awscredentials.NewStaticCredentialsProvider(settings.AccessKeyID, settings.SecretAccessKey, "")}, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(settings.Endpoint)
		options.UsePathStyle = true
	})
	pages := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(settings.Bucket)})
	result := map[string]string{}
	var total int64
	for pages.HasMorePages() {
		page, err := pages.NextPage(t.Context())
		require.NoError(t, err)
		for _, item := range page.Contents {
			require.Less(t, len(result), freshHistoryMaximumRecords)
			object, err := client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(settings.Bucket), Key: item.Key})
			require.NoError(t, err)
			hash := sha256.New()
			length, copyErr := io.Copy(hash, io.LimitReader(object.Body, freshHistoryMaximumBytes-total+1))
			require.NoError(t, object.Body.Close())
			require.NoError(t, copyErr)
			total += length
			require.LessOrEqual(t, total, int64(freshHistoryMaximumBytes))
			require.Equal(t, aws.ToInt64(item.Size), length)
			result[aws.ToString(item.Key)] = strconv.FormatInt(length, 10) + "/" + hex.EncodeToString(hash.Sum(nil))
		}
	}
	return result
}

func censusFreshHistoryFiles(t *testing.T, cfg *config.Config, request RecoveryActivationRequest) map[string]string {
	t.Helper()
	paths := cfg.EffectivePaths()
	roots := []string{paths.ConfigDir, paths.DataDir, paths.StateDir, paths.CacheDir, paths.RuntimeDir, catalogSettings(cfg).StateDirectory, request.Prepare.Directory, request.Prepare.FilesDirectory, request.History.HistoryDirectory, request.History.JournalDirectory, request.ActivationDirectory}
	result := map[string]string{}
	var total int64
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, exists := result[root]; exists {
			continue
		}
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			result[root] = "absent"
			continue
		}
		require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if _, exists := result[path]; exists {
				return nil
			}
			require.Less(t, len(result), freshHistoryMaximumRecords)
			info, err := entry.Info()
			if err != nil {
				return err
			}
			record := info.Mode().String() + "/" + strconv.FormatInt(info.Size(), 10) + "/" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
			if info.Mode().IsRegular() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				hash := sha256.New()
				length, copyErr := io.Copy(hash, io.LimitReader(file, 4*freshHistoryMaximumBytes-total+1))
				require.NoError(t, file.Close())
				require.NoError(t, copyErr)
				total += length
				require.LessOrEqual(t, total, int64(4*freshHistoryMaximumBytes))
				require.Equal(t, info.Size(), length)
				record += "/" + hex.EncodeToString(hash.Sum(nil))
			}
			result[path] = record
			return nil
		}))
	}
	return result
}
