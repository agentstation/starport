package app

import (
	"debug/buildinfo"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

// recoveryMeasurement is the version 1 measurement.json schema. It contains no endpoints or secrets.
// Times use UTC RFC 3339. Durations and signed capture ages use seconds.
// These laboratory observations do not approve production RPO or RTO limits.
type recoveryMeasurement struct {
	Version              int                 `json:"version"`
	RecordedAt           time.Time           `json:"recorded_at"`
	SourceHead           string              `json:"source_head"`
	SourceSHA256         map[string]string   `json:"source_sha256"`
	OperatorBinarySHA256 string              `json:"operator_binary_sha256"`
	OperatorGoVersion    string              `json:"operator_go_version"`
	OperatorRace         bool                `json:"operator_race"`
	OperatorModified     bool                `json:"operator_vcs_modified"`
	GoVersion            string              `json:"go_version"`
	GOOS                 string              `json:"goos"`
	GOARCH               string              `json:"goarch"`
	Hardware             measurementHardware `json:"hardware"`
	Topology             measurementTopology `json:"topology"`
	Limits               []string            `json:"limits"`
	Runs                 []*measurementRun   `json:"runs"`
}

// measurementHardware records the host and Docker engine resources without environment values.
type measurementHardware struct {
	HostModel         string   `json:"host_model"`
	HostCPUCount      int      `json:"host_cpu_count"`
	HostMemoryBytes   uint64   `json:"host_memory_bytes"`
	DockerCPUCount    int      `json:"docker_cpu_count"`
	DockerMemoryBytes uint64   `json:"docker_memory_bytes"`
	Unverified        []string `json:"unverified,omitempty"`
}

// measurementTopology names the native backends, immutable images, and test-owned Valkey persistence.
type measurementTopology struct {
	Backends          []string                    `json:"backends"`
	Containers        map[string]measurementImage `json:"containers"`
	ValkeyPersistence string                      `json:"valkey_persistence"`
	Placement         string                      `json:"placement"`
}

// measurementImage binds the image tag to the local immutable image ID.
type measurementImage struct {
	Image string `json:"image"`
	ID    string `json:"id"`
}

// measurementRun records one fenced deployment through fresh process readiness and state inspection.
// Complete stays false if any command, readiness probe, or state assertion fails.
type measurementRun struct {
	Scenario             string                      `json:"scenario"`
	Repeat               int                         `json:"repeat"`
	Complete             bool                        `json:"complete"`
	Load                 string                      `json:"load"`
	OutageStart          time.Time                   `json:"outage_start"`
	Commands             []measurementCommand        `json:"commands"`
	GatewayStart         time.Time                   `json:"gateway_start"`
	ReadinessRestored    time.Time                   `json:"readiness_restored"`
	ReadinessHTTPStatus  int                         `json:"readiness_http_status"`
	OutageToReadySeconds float64                     `json:"outage_to_ready_seconds"`
	StateObservedAt      time.Time                   `json:"state_observed_at"`
	OutageToStateSeconds float64                     `json:"outage_to_state_seconds"`
	Acknowledged         int                         `json:"acknowledged_writes"`
	Present              int                         `json:"present_writes"`
	Lost                 int                         `json:"lost_writes"`
	BackupStartedAt      time.Time                   `json:"backup_started_at"`
	BackupFinishedAt     time.Time                   `json:"backup_finished_at"`
	BackupAgeSeconds     float64                     `json:"backup_age_at_outage_seconds"`
	HistoryCapturedAt    time.Time                   `json:"history_captured_at"`
	HistoryThrough       time.Time                   `json:"history_through_utc"`
	HistoryAgeSeconds    float64                     `json:"history_age_at_outage_seconds"`
	BackupSHA256         string                      `json:"backup_sha256"`
	HistorySHA256        string                      `json:"history_sha256"`
	PreparedSHA256       string                      `json:"prepared_sha256"`
	DecisionSHA256       string                      `json:"decision_sha256"`
	InspectionSHA256     string                      `json:"inspection_sha256,omitempty"`
	Fencing              recovery.HistoryAttestation `json:"fencing"`
	FencingEvidence      string                      `json:"fencing_evidence"`
	FenceScope           string                      `json:"fence_scope"`
	PriorApproval        recovery.Record             `json:"prior_approval"`
	ClosedBoundary       recovery.Record             `json:"closed_boundary"`
	PreparedBoundary     recovery.Record             `json:"prepared_boundary"`
	RestoredApproval     recovery.Record             `json:"restored_approval"`
	ValkeyBefore         string                      `json:"valkey_before"`
	ValkeyRestored       string                      `json:"valkey_restored"`
	SQLSnapshotSHA256    string                      `json:"sql_snapshot_sha256"`
	BlobSnapshotSHA256   string                      `json:"blob_snapshot_sha256"`
}

// measurementCommand retains the command name, UTC interval, duration, status, and private output reference.
// Arguments and child environment values never enter this record.
type measurementCommand struct {
	Name            string    `json:"name"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationSeconds float64   `json:"duration_seconds"`
	ExitStatus      int       `json:"exit_status"`
	Output          string    `json:"private_output"`
}

func measurementMetadata(t *testing.T, binary string) recoveryMeasurement {
	t.Helper()
	head, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	info, err := buildinfo.ReadFile(binary)
	require.NoError(t, err, "cannot inspect the operator build")
	var revision string
	var race, modified bool
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "-race" {
			race = setting.Value == "true"
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value == "true"
		}
	}
	require.Equal(t, strings.TrimSpace(string(head)), revision, "operator binary must bind to the source head")
	body, err := os.ReadFile(binary)
	require.NoError(t, err)
	record := recoveryMeasurement{
		Version: 1, RecordedAt: time.Now().UTC(), SourceHead: revision,
		SourceSHA256: make(map[string]string), OperatorBinarySHA256: canonicalRecordSHA256(body),
		OperatorGoVersion: info.GoVersion, OperatorRace: race, OperatorModified: modified, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Hardware: measurementHardware{HostCPUCount: runtime.NumCPU()},
		Topology: measurementTopology{Backends: []string{"valkey", "postgresql", "s3"}, Containers: make(map[string]measurementImage),
			ValkeyPersistence: "appendonly yes; appendfsync always; save disabled",
			Placement:         "gateway and operator on host; native backends in local Docker engine; isolated schemas and buckets; private primary and replica"},
		Limits: []string{"idle laboratory deployment; three repetitions per scenario; no production limits approved",
			"counts cover explicit Valkey probe writes only; not every domain record or provider acknowledgement",
			"capture and synthetic final-only history occur after the fence; negative age means after outage start",
			"source fleet setup is outside the outage interval; operator commands and fresh HTTP readiness are inside",
			"the test owns every writer; no external traffic, provider load, or worker dispatch load"},
	}
	files, err := filepath.Glob("recovery*_test.go")
	require.NoError(t, err)
	files = append(files, "restore_test.go", "backup_test.go", "inspect_import.go", "inspect_import_fleet_test.go")
	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		record.SourceSHA256["internal/app/"+file] = canonicalRecordSHA256(body)
	}
	switch runtime.GOOS {
	case "darwin":
		model, modelErr := exec.CommandContext(t.Context(), "sysctl", "-n", "hw.model").Output()
		if modelErr != nil {
			record.Hardware.Unverified = append(record.Hardware.Unverified, "UNVERIFIED: host model sysctl query failed")
		} else {
			record.Hardware.HostModel = strings.TrimSpace(string(model))
		}
		memory, memoryErr := exec.CommandContext(t.Context(), "sysctl", "-n", "hw.memsize").Output()
		if memoryErr != nil {
			record.Hardware.Unverified = append(record.Hardware.Unverified, "UNVERIFIED: host memory sysctl query failed")
		} else {
			record.Hardware.HostMemoryBytes, err = strconv.ParseUint(strings.TrimSpace(string(memory)), 10, 64)
			require.NoError(t, err)
		}
	case "linux":
		model, err := os.ReadFile("/sys/devices/virtual/dmi/id/product_name")
		require.NoError(t, err, "host model metadata is unavailable")
		record.Hardware.HostModel = strings.TrimSpace(string(model))
		memory, err := os.ReadFile("/proc/meminfo")
		require.NoError(t, err)
		for line := range strings.SplitSeq(string(memory), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				require.Len(t, fields, 3)
				require.Equal(t, "kB", fields[2])
				kib, err := strconv.ParseUint(fields[1], 10, 64)
				require.NoError(t, err)
				record.Hardware.HostMemoryBytes = kib * 1024
			}
		}
		require.Positive(t, record.Hardware.HostMemoryBytes)
	default:
		t.Fatal("host hardware metadata needs macOS sysctl or Linux procfs")
	}
	if race {
		record.Limits = append(record.Limits, "race instrumentation and child process exit overhead affect all command durations")
	}
	record.Limits = append(record.Limits, "product records name the Valkey backend identity; SQL and S3 retain snapshot digests, not independent backend IDs")
	docker := measurementSystemValue(t, "docker", "info", "--format", "{{.NCPU}} {{.MemTotal}}")
	fields := strings.Fields(docker)
	require.Len(t, fields, 2)
	record.Hardware.DockerCPUCount, err = strconv.Atoi(fields[0])
	require.NoError(t, err)
	record.Hardware.DockerMemoryBytes, err = strconv.ParseUint(fields[1], 10, 64)
	require.NoError(t, err)
	for role, name := range map[string]string{"postgresql": "starport-csp13-postgres", "s3": "starport-csp122-retirement-objectstore"} {
		record.Topology.Containers[role] = measurementContainerImage(t, name)
	}
	return record
}

func measurementSystemValue(t *testing.T, program string, arguments ...string) string {
	t.Helper()
	body, err := exec.CommandContext(t.Context(), program, arguments...).Output() // #nosec G204 -- Only fixed hardware and image queries call this helper.
	require.NoError(t, err, "measurement metadata query failed")
	return strings.TrimSpace(string(body))
}

func measurementContainerImage(t *testing.T, name string) measurementImage {
	t.Helper()
	value := measurementSystemValue(t, "docker", "inspect", "--format", "{{.Config.Image}} {{.Image}}", name)
	fields := strings.Fields(value)
	require.Len(t, fields, 2)
	return measurementImage{Image: fields[0], ID: fields[1]}
}

func measurementWrite(t *testing.T, directory string, record recoveryMeasurement) {
	t.Helper()
	body, err := json.Marshal(record, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "measurement.json"), body, 0600))
}
