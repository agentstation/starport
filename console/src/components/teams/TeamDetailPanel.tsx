import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useState } from "react";

import { BudgetLine } from "@/components/ui/BudgetLine";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogError,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DestructiveButton,
  Field,
  GhostButton,
  INPUT_CLASS,
  PrimaryButton,
  RowAction,
} from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { RelativeTime } from "@/components/ui/RelativeTime";
import {
  addTeamMember,
  createAccountGrant,
  deleteAccountGrant,
  removeTeamMember,
  updateTeam,
  type Team,
  type TeamBudget,
} from "@/lib/api";
import { formatNanoUSD } from "@/lib/format";
import { queries } from "@/lib/queries";
import { report } from "@/lib/mutations";

// TeamDetailPanel governs one team: who is on the roster and which accounts
// the team grants. Both lists are the gateway's — every edit travels before
// it shows, so what the operator reads here is what the grant resolution
// actually uses.

export function TeamDetailPanel({
  team,
  onClose,
}: {
  team: Team;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [draftMember, setDraftMember] = useState("");
  const [draftAccount, setDraftAccount] = useState("");
  const [draftBudget, setDraftBudget] = useState(
    team.budget ? String(team.budget.limit / 1e9) : "",
  );
  const [draftInterval, setDraftInterval] = useState(
    team.budget?.interval ?? "month",
  );

  const roster = useQuery({
    ...queries.teamMembers(team.id),
  });
  const grants = useQuery({
    ...queries.teamGrants(team.id),
  });
  const members = useQuery({
    ...queries.members(),
  });
  const accounts = useQuery({
    ...queries.accounts(),
  });

  const add = useMutation({
    mutationFn: (userId: string) => addTeamMember(team.id, userId),
    onSuccess: async () => {
      setDraftMember("");
      await queryClient.invalidateQueries({ queryKey: queries.teamMembers(team.id).queryKey });
    },
    onError: (error) =>
      report(`Add failed: ${error instanceof Error ? error.message : error}`),
  });

  const drop = useMutation({
    mutationFn: (userId: string) => removeTeamMember(team.id, userId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queries.teamMembers(team.id).queryKey });
    },
    onError: (error) =>
      report(`Remove failed: ${error instanceof Error ? error.message : error}`),
  });

  const grant = useMutation({
    mutationFn: (accountId: string) =>
      createAccountGrant({ account_id: accountId, team_id: team.id }),
    onSuccess: async () => {
      setDraftAccount("");
      await queryClient.invalidateQueries({ queryKey: queries.teamGrants(team.id).queryKey });
    },
    onError: (error) =>
      report(`Grant failed: ${error instanceof Error ? error.message : error}`),
  });

  const revoke = useMutation({
    mutationFn: (accountId: string) =>
      deleteAccountGrant({ account_id: accountId, team_id: team.id }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queries.teamGrants(team.id).queryKey });
    },
    onError: (error) =>
      report(`Remove failed: ${error instanceof Error ? error.message : error}`),
  });

  // Every budget write names the revision this panel read, when the
  // gateway gave one, so a save cannot overwrite a rename another operator
  // landed in between: the gateway refuses it and the operator re-reads.
  const revision = team.revision;
  const saveBudget = useMutation({
    mutationFn: (budget: TeamBudget) =>
      updateTeam(team.id, {
        name: team.name,
        budget,
        ...(revision !== undefined ? { revision } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queries.teams().queryKey });
    },
    onError: (error) =>
      report(`Budget save failed: ${error instanceof Error ? error.message : error}`),
  });

  // submitBudget reads the draft: it must be a positive dollar amount.
  // Clearing is its own action below, so an emptied field never silently
  // unmeters the team.
  const [budgetError, setBudgetError] = useState<string | null>(null);
  const [removingBudget, setRemovingBudget] = useState(false);
  const submitBudget = () => {
    setBudgetError(null);
    if (!draftBudget.trim()) {
      setBudgetError(
        team.budget
          ? "Enter an amount, or remove the budget below."
          : "Enter an amount to set a budget.",
      );
      return;
    }
    const usd = Number(draftBudget);
    if (!Number.isFinite(usd) || usd <= 0) {
      setBudgetError("The spend budget must be a positive amount.");
      return;
    }
    saveBudget.mutate({
      limit: Math.round(usd * 1e9),
      interval: draftInterval,
    });
  };

  const memberById = new Map(
    (members.data ?? []).map((member) => [member.id, member]),
  );
  const onRoster = new Set((roster.data ?? []).map((row) => row.user_id));
  const addable = (members.data ?? []).filter(
    (member) => !onRoster.has(member.id),
  );
  const granted = new Set((grants.data ?? []).map((row) => row.account_id));
  const grantable = (accounts.data ?? []).filter(
    (account) => !granted.has(account.id),
  );

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{team.name}</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <div className="flex flex-col gap-5">
            <div className="flex flex-col gap-1">
              <span className="text-xs font-medium text-text-2">Team ID</span>
              <span className="font-mono text-sm text-text-1">{team.id}</span>
              <span className="text-xs text-text-4">
                created <RelativeTime iso={team.created_at} />
              </span>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">Spend budget</span>
              <span className="text-xs text-text-4">
                Bounds what every key attributed to this team spends together,
                across every account the team reaches.
              </span>
              {team.budget && team.budgets?.spend && (
                <BudgetLine usage={team.budgets.spend} render={formatNanoUSD} unit="spend" />
              )}
              <div className="flex items-end gap-2">
                <Field label="Budget (USD)">
                  <div className="flex gap-2">
                    <input
                      type="number"
                      min="0"
                      step="0.01"
                      value={draftBudget}
                      onChange={(event) => setDraftBudget(event.target.value)}
                      placeholder="unmetered"
                      aria-label="Team spend budget (USD)"
                      className={`${INPUT_CLASS} w-32`}
                    />
                    <Select
                      value={draftInterval}
                      onChange={(event) => setDraftInterval(event.target.value)}
                      aria-label="Team budget interval"
                    >
                      <option value="day">per day</option>
                      <option value="week">per week</option>
                      <option value="month">per month</option>
                    </Select>
                  </div>
                </Field>
                <PrimaryButton
                  onClick={submitBudget}
                  disabled={saveBudget.isPending}
                >
                  Save budget
                </PrimaryButton>
              </div>
              {budgetError && <p className="text-xs text-error">{budgetError}</p>}
              {team.budget && (
                <div>
                  <RowAction
                    onClick={() => setRemovingBudget(true)}
                    disabled={saveBudget.isPending}
                  >
                    Remove budget
                  </RowAction>
                </div>
              )}
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">Roster</span>
              {roster.data?.length === 0 && (
                <span className="text-sm text-text-3">Nobody is on this team.</span>
              )}
              <ul className="flex flex-col gap-1">
                {(roster.data ?? []).map((row) => {
                  const member = memberById.get(row.user_id);
                  return (
                    <li
                      key={row.user_id}
                      data-testid="team-member-row"
                      className="flex items-center justify-between gap-2 rounded-sm border border-border-1 bg-bg-panel px-3 py-2 text-sm"
                    >
                      <span className="truncate text-text-1">
                        {member?.display_name || member?.email || row.user_id}
                      </span>
                      <RowAction
                        onClick={() => drop.mutate(row.user_id)}
                        disabled={drop.isPending}
                        aria-label={`Remove ${row.user_id} from the team`}
                      >
                        remove
                      </RowAction>
                    </li>
                  );
                })}
              </ul>
              <div className="flex items-end gap-2">
                <Field label="Add a member">
                  <Select
                    value={draftMember}
                    onChange={(event) => setDraftMember(event.target.value)}
                    aria-label="Member to add"
                  >
                    <option value="">Choose a member…</option>
                    {addable.map((member) => (
                      <option key={member.id} value={member.id}>
                        {member.display_name || member.email || member.subject}
                      </option>
                    ))}
                  </Select>
                </Field>
                <PrimaryButton
                  onClick={() => draftMember && add.mutate(draftMember)}
                  disabled={!draftMember || add.isPending}
                >
                  <Plus className="size-4" />
                  Add
                </PrimaryButton>
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">
                Account grants
              </span>
              {grants.data?.length === 0 && (
                <span className="text-sm text-text-3">
                  This team grants no account yet.
                </span>
              )}
              <ul className="flex flex-col gap-1">
                {(grants.data ?? []).map((row) => (
                  <li
                    key={row.account_id}
                    data-testid="team-grant-row"
                    className="flex items-center justify-between gap-2 rounded-sm border border-border-1 bg-bg-panel px-3 py-2 text-sm"
                  >
                    <span className="truncate font-mono text-xs text-text-1">
                      {row.account_id}
                    </span>
                    <RowAction
                      onClick={() => revoke.mutate(row.account_id)}
                      disabled={revoke.isPending}
                      aria-label={`Remove the ${row.account_id} grant`}
                    >
                      remove
                    </RowAction>
                  </li>
                ))}
              </ul>
              <div className="flex items-end gap-2">
                <Field
                  label="Grant an account"
                  hint="Everyone on the roster reaches it, now and later."
                >
                  <Select
                    value={draftAccount}
                    onChange={(event) => setDraftAccount(event.target.value)}
                    aria-label="Account to grant"
                  >
                    <option value="">Choose an account…</option>
                    {grantable.map((account) => (
                      <option key={account.id} value={account.id}>
                        {account.name || account.id}
                      </option>
                    ))}
                  </Select>
                </Field>
                <PrimaryButton
                  onClick={() => draftAccount && grant.mutate(draftAccount)}
                  disabled={!draftAccount || grant.isPending}
                >
                  <Plus className="size-4" />
                  Grant
                </PrimaryButton>
              </div>
            </div>
          </div>
        </SheetBody>
      </SheetContent>
      {removingBudget && (
        <RemoveBudgetModal
          team={team}
          onClose={() => setRemovingBudget(false)}
          onRemoved={async () => {
            setRemovingBudget(false);
            setDraftBudget("");
            await queryClient.invalidateQueries({ queryKey: queries.teams().queryKey });
          }}
        />
      )}
    </Sheet>
  );
}

// RemoveBudgetModal restates the team and the ceiling before the team goes
// unmetered (DESIGN.md destructive-modal contract). The write states the
// team whole without a budget, at the revision this panel read.
function RemoveBudgetModal({
  team,
  onClose,
  onRemoved,
}: {
  team: Team;
  onClose: () => void;
  onRemoved: () => void;
}) {
  const [error, setError] = useState("");
  const remove = useMutation({
    mutationFn: () =>
      updateTeam(team.id, {
        name: team.name,
        ...(team.revision !== undefined ? { revision: team.revision } : {}),
      }),
    onSuccess: onRemoved,
    onError: (problem) =>
      setError(`Remove failed: ${problem instanceof Error ? problem.message : problem}`),
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove budget</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <p className="text-sm text-text-2">
            Remove the{" "}
            <strong className="text-text-1">
              {team.budget ? `${formatNanoUSD(team.budget.limit)} per ${team.budget.interval}` : ""}
            </strong>{" "}
            budget from <strong className="text-text-1">{team.name}</strong>? Keys
            attributed to the team spend without a team ceiling from the next
            request on.
          </p>
        </DialogBody>
        <DialogError>{error}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <DestructiveButton onClick={() => remove.mutate()} disabled={remove.isPending}>
            Remove budget
          </DestructiveButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
