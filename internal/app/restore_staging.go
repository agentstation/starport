package app

import (
	"context"
	"errors"
	"os"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
)

const retainedStagingReason = "Publication evidence remains in the verified backup and inactive preparation. Staging bytes never replace destination records."

func retainedRestoreInputs(source *recovery.RestoreSource, tree recovery.FileTreeRequest) (map[string]productfiles.RetainedFile, productfiles.RetainedRecordReader, error) {
	artifacts := source.SelectedFileArtifacts()
	files := make(map[string]string, len(tree.Files))
	retained := make(map[string]productfiles.RetainedFile, len(tree.Files))
	for _, file := range tree.Files {
		artifact, found := artifacts[file.ArtifactID]
		if !found || files[file.Relative] != "" {
			return nil, nil, errors.New("restore selection contains an unknown or duplicate file")
		}
		files[file.Relative] = file.ArtifactID
		retained[file.Relative] = productfiles.RetainedFile{Size: artifact.Size, SHA256: artifact.SHA256}
	}
	read := func(ctx context.Context, name string, limit int64) ([]byte, error) {
		id, found := files[name]
		if !found {
			return nil, os.ErrNotExist
		}
		return source.SelectedFile(ctx, id, limit)
	}
	return retained, read, nil
}

func selectCredentialRestoreFiles(ctx context.Context, cfg *config.Config, role string, source *recovery.RestoreSource, tree recovery.FileTreeRequest, plan, remaining []recovery.FileDisposition) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	files, read, err := retainedRestoreInputs(source, tree)
	if err != nil {
		return tree, nil, err
	}
	var inactive []string
	if role == config.AcquisitionPolicyRole {
		inactive, err = catalogSettings(cfg).InspectCredentialPolicyPublications(ctx, files, read)
	} else {
		inactive, err = credentials.InspectSelectionPolicyPublications(ctx, files, read)
	}
	if err != nil {
		return tree, nil, err
	}
	return selectInactiveRestoreFiles(tree, plan, remaining, role, inactive, "verified-staging", retainedStagingReason)
}

func selectInactiveRestoreFiles(tree recovery.FileTreeRequest, plan, remaining []recovery.FileDisposition, role string, inactive []string, action, reason string) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	present := make(map[string]bool, len(tree.Files))
	for _, file := range tree.Files {
		present[file.Relative] = true
	}
	selected := make(map[string]bool, len(inactive))
	for _, name := range inactive {
		if !present[name] || selected[name] {
			return tree, nil, errors.New("file owner selected unknown or duplicate inactive evidence")
		}
		selected[name] = true
	}
	active := make([]recovery.FileTreeFile, 0, len(tree.Files)-len(inactive))
	for _, file := range tree.Files {
		if !selected[file.Relative] {
			active = append(active, file)
		}
	}
	for _, file := range plan {
		if file.Role == role && selected[file.Relative] {
			file.Destination = ""
			file.Action = action
			file.Reason = reason
			remaining = append(remaining, file)
		}
	}
	tree.Files = active
	return tree, remaining, nil
}
