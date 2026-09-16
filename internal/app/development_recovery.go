package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	developmentRecoveryEntryLimit   = 16384
	developmentRecoveryBatchSize    = 128
	developmentRecoverySessionLimit = 256
)

type developmentRecoveryReport struct {
	Recovered      int
	Live           int
	Preserved      int
	PreservedPaths []string
	Limited        bool
}

func recoverDevelopmentScratch(ctx context.Context, temporary string) (report developmentRecoveryReport, resultErr error) {
	parent, err := os.Open(temporary) //nolint:gosec // The host selects its operating system temporary directory.
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	entries, sessions := 0, 0
	for entries < developmentRecoveryEntryLimit && sessions < developmentRecoverySessionLimit {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		batch, err := parent.ReadDir(developmentRecoveryBatchSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return report, err
		}
		entries += len(batch)
		for _, entry := range batch {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if !strings.HasPrefix(entry.Name(), developmentScratchPrefix) {
				continue
			}
			if sessions == developmentRecoverySessionLimit {
				report.Limited = true
				return report, nil
			}
			sessions++
			if !entry.IsDir() {
				report.preserve(filepath.Join(temporary, entry.Name()))
				continue
			}
			session, err := readDevelopmentScratch(filepath.Join(temporary, entry.Name()))
			if err != nil {
				report.preserve(filepath.Join(temporary, entry.Name()))
				continue
			}
			locked, err := session.acquire()
			if err != nil {
				report.preserve(filepath.Join(temporary, entry.Name()))
			} else if !locked {
				report.Live++
			} else if err := session.close(); err != nil {
				report.preserve(filepath.Join(temporary, entry.Name()))
			} else {
				report.Recovered++
			}
		}
		if errors.Is(err, io.EOF) {
			return report, nil
		}
	}
	report.Limited = true
	return report, nil
}

func (r *developmentRecoveryReport) preserve(path string) {
	r.Preserved++
	r.PreservedPaths = append(r.PreservedPaths, path)
}

func readDevelopmentScratch(path string) (*developmentScratch, error) {
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return nil, err
	}
	metadata, err := directory.ExistingChild(developmentScratchOwner)
	if err != nil {
		return nil, err
	}
	encoded, err := metadata.ReadFile(developmentScratchRecordName, developmentScratchRecordLimit)
	if err != nil {
		return nil, err
	}
	var record developmentScratchRecord
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errDevelopmentScratchChanged
	}
	if record.Version != developmentScratchRecordVersion || uuid.Validate(record.Session) != nil ||
		record.Name != filepath.Base(path) || record.Root == "" || len(record.Directories) != len(developmentScratchDirectories()) ||
		record.Metadata[developmentScratchLock] == "" || len(record.Metadata) > developmentMetadataEntryLimit {
		return nil, errDevelopmentScratchChanged
	}
	for _, name := range developmentScratchDirectories() {
		if record.Directories[name] == "" {
			return nil, errDevelopmentScratchChanged
		}
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return nil, errors.Join(errDevelopmentScratchChanged, err)
	}
	return &developmentScratch{path: path, directory: directory, metadata: metadata, record: record, encoded: encoded, published: true}, nil
}

func prepareDevelopmentScratch(ctx context.Context) (*developmentScratch, error) {
	temporary := os.TempDir()
	report, err := recoverDevelopmentScratch(ctx, temporary)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil || report.Preserved != 0 || report.Limited {
		log.Warn().Err(err).Int("preserved", report.Preserved).Strs("directories", report.PreservedPaths).Bool("scan_limited", report.Limited).
			Msg("development scratch recovery preserved directories without verified ownership")
	}
	return newDevelopmentScratch(ctx, temporary)
}
