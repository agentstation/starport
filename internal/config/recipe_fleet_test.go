package config

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/agentstation/starport/internal/deployment"
	"github.com/agentstation/starport/internal/storage"
)

// The storage tests use these MinIO fixture credentials.
const (
	fleetRecipeAccessKeyID     = "starport-test"
	fleetRecipeSecretAccessKey = "starport-local-test-only"
	fleetRecipeRegion          = "us-east-1"
)

// fleetRecipeTarget names one isolated set of shared stores.
type fleetRecipeTarget struct {
	ValkeyURL   string `json:"valkey_url"`
	PostgresURL string `json:"postgres_url"`
	Bucket      string `json:"bucket"`
}

// fleetRecipeInputs is the private input file of the fleet harness mode.
type fleetRecipeInputs struct {
	DeploymentID      string            `json:"deployment_id"`
	ValkeyIncarnation string            `json:"valkey_incarnation"`
	Endpoint          string            `json:"object_store_endpoint"`
	AccessKeyID       string            `json:"object_store_access_key_id"`
	SecretAccessKey   string            `json:"object_store_secret_access_key"`
	Region            string            `json:"object_store_region"`
	Source            fleetRecipeTarget `json:"source"`
	Restore           fleetRecipeTarget `json:"restore"`
}

// TestFleetRecipeContainerRecreation runs two fleet gateways from the recipe
// image against fresh Valkey databases, PostgreSQL schemas, and buckets. It
// proves that gateway keys, provider credentials, and files survive the
// replacement of both gateway containers.
func TestFleetRecipeContainerRecreation(t *testing.T) {
	image := os.Getenv("STARPORT_RECIPE_IMAGE")
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	postgresURL := os.Getenv("TEST_POSTGRES_URL")
	endpoint := os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if image == "" || valkeyURL == "" || postgresURL == "" || endpoint == "" {
		t.Skip("STARPORT_RECIPE_IMAGE, TEST_VALKEY_URL, TEST_POSTGRES_URL, and TEST_BLOB_S3_ENDPOINT are required for fleet recipe qualification")
	}
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	inputs := fleetRecipeInputs{
		DeploymentID:    "recipe-" + strings.ToLower(rand.Text()[:12]),
		Endpoint:        endpoint,
		AccessKeyID:     fleetRecipeAccessKeyID,
		SecretAccessKey: fleetRecipeSecretAccessKey,
		Region:          fleetRecipeRegion,
	}
	databases := emptyValkeyDatabases(ctx, t, valkeyURL, inputs.DeploymentID, 2)
	inputs.Source = fleetRecipeTarget{
		ValkeyURL:   valkeyDatabaseURL(t, valkeyURL, databases[0]),
		PostgresURL: freshPostgresSchema(ctx, t, postgresURL),
		Bucket:      freshObjectStoreBucket(ctx, t, endpoint),
	}
	inputs.Restore = fleetRecipeTarget{
		ValkeyURL:   valkeyDatabaseURL(t, valkeyURL, databases[1]),
		PostgresURL: freshPostgresSchema(ctx, t, postgresURL),
		Bucket:      freshObjectStoreBucket(ctx, t, endpoint),
	}
	inputs.ValkeyIncarnation = valkeyIncarnation(t, inputs.Restore.ValkeyURL, inputs.DeploymentID)
	body, err := json.Marshal(inputs)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "fleet-inputs.json")
	require.NoError(t, os.WriteFile(path, body, 0o600))

	cmd := exec.CommandContext(ctx, "python3", filepath.Join(repository, "scripts/test-storage-recipes.py"),
		"--image", image, "--mode", "fleet", "--fleet-inputs", path)
	output, err := cmd.Output()
	// The script hides command output, so a failure names only the step.
	require.NoError(t, err, "%s", exitDetail(err))
	var result struct {
		Status       string   `json:"status"`
		Observations []string `json:"observations"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, "PASS", result.Status)
	require.Equal(t, []string{
		"fleet_records_created_through_one_replica",
		"fleet_records_read_through_other_replica",
		"fleet_gateway_containers_replaced",
		"fleet_records_survive_container_recreation",
		"fleet_catalog_source_embedded_without_github",
		"fleet_configuration_migrate_round_trip",
		"fleet_catalog_source_file_without_github",
		"fleet_backup_verifies_records",
		"fleet_restore_prepares_fresh_targets",
		"fleet_restore_import_inspected",
		"fleet_restore_history_written",
		"fleet_restore_activated",
	}, result.Observations)
	t.Log(string(output))
}

// exitDetail returns the stderr of a failed command without other output.
func exitDetail(err error) string {
	if exit, ok := err.(*exec.ExitError); ok {
		return string(exit.Stderr)
	}
	return ""
}

// emptyValkeyDatabases selects databases that hold no keys. Other runs share
// the fixture, so cleanup deletes only the keys of this deployment.
func emptyValkeyDatabases(ctx context.Context, t *testing.T, base, deploymentID string, count int) []int {
	t.Helper()
	prefix, err := deployment.KeyPrefix(deploymentID)
	require.NoError(t, err)
	var selected []int
	for database := 1; database < 16 && len(selected) < count; database++ {
		option, err := valkey.ParseURL(valkeyDatabaseURL(t, base, database))
		require.NoError(t, err)
		client, err := valkey.NewClient(option)
		require.NoError(t, err)
		size, err := client.Do(ctx, client.B().Dbsize().Build()).AsInt64()
		client.Close()
		require.NoError(t, err)
		if size != 0 {
			continue
		}
		selected = append(selected, database)
		t.Cleanup(func() { deleteValkeyPrefix(t, option, "{"+prefix+"}*") })
	}
	require.Len(t, selected, count, "the Valkey fixture has too few empty databases")
	return selected
}

func deleteValkeyPrefix(t *testing.T, option valkey.ClientOption, pattern string) {
	client, err := valkey.NewClient(option)
	require.NoError(t, err)
	defer client.Close()
	ctx := context.Background()
	var cursor uint64
	for {
		entry, err := client.Do(ctx, client.B().Scan().Cursor(cursor).Match(pattern).Count(1000).Build()).AsScanEntry()
		require.NoError(t, err)
		if len(entry.Elements) > 0 {
			require.NoError(t, client.Do(ctx, client.B().Del().Key(entry.Elements...).Build()).Error())
		}
		if cursor = entry.Cursor; cursor == 0 {
			return
		}
	}
}

func valkeyDatabaseURL(t *testing.T, base string, database int) string {
	t.Helper()
	parsed, err := url.Parse(base)
	require.NoError(t, err)
	parsed.Path = fmt.Sprintf("/%d", database)
	return parsed.String()
}

func valkeyIncarnation(t *testing.T, address, deploymentID string) string {
	t.Helper()
	store, err := storage.OpenValkey(storage.ValkeyConfig{DeploymentID: deploymentID, URL: address, AllowInsecure: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	provider, ok := store.(storage.IncarnationProvider)
	require.True(t, ok, "the Valkey store has no incarnation")
	incarnation, err := provider.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	return incarnation
}

// freshPostgresSchema creates an empty schema and returns a URL that selects it.
func freshPostgresSchema(ctx context.Context, t *testing.T, base string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, base)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close(context.Background())) }()
	schema := "recipe_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{schema}.Sanitize()
	_, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), base)
		require.NoError(t, err)
		defer func() { require.NoError(t, conn.Close(context.Background())) }()
		_, err = conn.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(base)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// freshObjectStoreBucket creates an empty bucket and removes it after the test.
func freshObjectStoreBucket(ctx context.Context, t *testing.T, endpoint string) string {
	t.Helper()
	client := s3.NewFromConfig(aws.Config{
		Region:      fleetRecipeRegion,
		Credentials: awscredentials.NewStaticCredentialsProvider(fleetRecipeAccessKeyID, fleetRecipeSecretAccessKey, ""),
	}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	bucket := "recipe-" + strings.ToLower(rand.Text())
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.Background()
		pages := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			require.NoError(t, err)
			for _, object := range page.Contents {
				_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: object.Key})
				require.NoError(t, err)
			}
		}
		_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
		require.NoError(t, err)
	})
	return bucket
}
