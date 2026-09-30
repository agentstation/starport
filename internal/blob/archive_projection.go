package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ArchiveProjection changes only a private copy of an original verified archive.
// It exposes no configured store, native recovery claim, or activation permission.
type ArchiveProjection struct{ state *archiveProjectionState }

type archiveProjectionState struct {
	view    *snapshotView
	scratch string
}

// Format omits private paths and retained bytes.
func (ArchiveProjection) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "<private offline blob projection>")
}

// OpenArchiveProjection verifies the original envelope and copies its complete retained state.
func OpenArchiveProjection(ctx context.Context, source, scratch string, expected Snapshot) (*ArchiveProjection, error) {
	if ctx == nil {
		return nil, ErrCorruptPublication
	}
	opened, err := OpenSnapshot(ctx, source, scratch, expected)
	if err != nil {
		return nil, err
	}
	return &ArchiveProjection{state: &archiveProjectionState{view: opened.(*snapshotView), scratch: scratch}}, nil
}

// ApplyPublication validates exact historical preimages before changing private bytes.
// It writes no import claim, native cursor, or activation receipt.
func (p *ArchiveProjection) ApplyPublication(ctx context.Context, key string, expected, next PublicationState, input io.Reader) error {
	if ctx == nil || p == nil || p.state == nil || p.state.view == nil || !validPublicationTransition(key, expected, next) {
		return ErrPublicationExists
	}
	actual, err := p.state.view.InspectPublication(ctx, key)
	if err != nil || actual != expected {
		return errors.Join(ErrPublicationExists, err)
	}
	staged, cleanup, err := stageReplayPayload(ctx, input, next, p.state.scratch)
	if err != nil {
		return err
	}
	defer cleanup()
	var stream io.Reader
	if staged != nil {
		stream = staged
	}
	return replayRetainedBytes(ctx, p.state.view.store, ImportPublicationStep{Key: key, Expected: expected, Next: next}, stream)
}

// ReadPublished reads the private projected state for domain reference checks.
func (p *ArchiveProjection) ReadPublished(ctx context.Context, key string) (io.ReadCloser, error) {
	if ctx == nil || p == nil || p.state == nil || p.state.view == nil {
		return nil, ErrCorruptPublication
	}
	return p.state.view.ReadPublished(ctx, key)
}

// StatPublished reads private projected metadata without publishing bytes.
func (p *ArchiveProjection) StatPublished(ctx context.Context, key string) (Info, error) {
	if ctx == nil || p == nil || p.state == nil || p.state.view == nil {
		return Info{}, ErrCorruptPublication
	}
	return p.state.view.StatPublished(ctx, key)
}

// Snapshot writes a complete portable image of private projected bytes and retirements.
func (p *ArchiveProjection) Snapshot(ctx context.Context, destination string) (Snapshot, error) {
	if ctx == nil || p == nil || p.state == nil || p.state.view == nil {
		return Snapshot{}, ErrCorruptPublication
	}
	return Backup(ctx, p.state.view.store, destination)
}

// Close removes only the owned private archive copy.
func (p *ArchiveProjection) Close() error {
	if p == nil || p.state == nil || p.state.view == nil {
		return nil
	}
	err := p.state.view.Close()
	p.state.view = nil
	return err
}
