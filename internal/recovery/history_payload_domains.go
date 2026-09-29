package recovery

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

func prepareHistoryDomains(ctx context.Context, before *KVSnapshotView, assets blob.PublicationReader, at time.Time, encryption *credentials.EncryptionService, value historyKVDomain, proposal *historyKVProposal) error {
	if len(value.Accounts) > 16 || len(value.Credentials) > 16 || len(value.Batches) > 16 || len(value.Videos) > 16 || len(value.Slots) > 64 || len(value.Files) > 64 {
		return ErrConflict
	}
	if err := prepareHistoryPolicy(ctx, before, encryption, value, proposal); err != nil {
		return err
	}
	if len(value.Attempts) > 0 || len(value.Corrections) > 0 {
		changes, err := reservation.PrepareAccountingReplay(ctx, before, value.Attempts, value.Corrections)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	if len(value.Slots) > 0 {
		changes, err := jobslots.PrepareClaimReplay(ctx, before, value.Slots)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	if len(value.Files) > 0 {
		changes, err := files.PrepareRecoveryReplay(ctx, before, assets, at, value.Files)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	for _, video := range value.Videos {
		if len(video.Corrections) == 0 && !video.Publish {
			return ErrConflict
		}
		if len(video.Corrections) > 0 {
			changes, err := jobs.PrepareJobCorrectionReplay(ctx, before, video.Final, video.Corrections)
			if err := appendHistoryChanges(proposal, changes, err); err != nil {
				return err
			}
		}
	}
	for _, batch := range value.Batches {
		changes, err := jobs.PrepareBatchReplay(ctx, proposal, batch)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	for _, video := range value.Videos {
		if video.Publish {
			change, err := jobs.PrepareJobReplay(ctx, before, proposal, assets, at, video.Final)
			if err := appendHistoryChanges(proposal, []storage.CompareAndSwapMutation{change}, err); err != nil {
				return err
			}
		}
	}
	return nil
}
func prepareHistoryPolicy(ctx context.Context, before *KVSnapshotView, encryption *credentials.EncryptionService, value historyKVDomain, proposal *historyKVProposal) error {
	if len(value.Accounts) > 0 {
		changes, err := account.PrepareRecoveryReplay(ctx, before, value.Accounts)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	if value.APIKeys != nil {
		changes, err := apikey.PrepareRecoveryReplay(ctx, before, *value.APIKeys)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	if len(value.Credentials) > 0 {
		changes, err := credentials.PrepareRecoveryReplay(ctx, before, encryption, value.Credentials)
		if err := appendHistoryChanges(proposal, changes, err); err != nil {
			return err
		}
	}
	return nil
}
func appendHistoryChanges(proposal *historyKVProposal, changes []storage.CompareAndSwapMutation, err error) error {
	if err != nil {
		return err
	}
	return proposal.add(changes)
}
