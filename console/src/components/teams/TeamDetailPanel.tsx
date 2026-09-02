import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useState } from "react";

import { Field, INPUT_CLASS, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { SidePanel } from "@/components/ui/SidePanel";
import {
  addTeamMember,
  createAccountGrant,
  deleteAccountGrant,
  removeTeamMember,
  updateTeam,
  type Team,
  type TeamBudget,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import { formatRelativeTime } from "@/lib/format";

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
  const [notice, setNotice] = useState<string | null>(null);
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
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: queries.teamMembers(team.id).queryKey });
    },
    onError: (error) =>
      setNotice(
        `Add failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const drop = useMutation({
    mutationFn: (userId: string) => removeTeamMember(team.id, userId),
    onSuccess: async () => {
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: queries.teamMembers(team.id).queryKey });
    },
    onError: (error) =>
      setNotice(
        `Remove failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const grant = useMutation({
    mutationFn: (accountId: string) =>
      createAccountGrant({ account_id: accountId, team_id: team.id }),
    onSuccess: async () => {
      setDraftAccount("");
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: queries.teamGrants(team.id).queryKey });
    },
    onError: (error) =>
      setNotice(
        `Grant failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const revoke = useMutation({
    mutationFn: (accountId: string) =>
      deleteAccountGrant({ account_id: accountId, team_id: team.id }),
    onSuccess: async () => {
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: queries.teamGrants(team.id).queryKey });
    },
    onError: (error) =>
      setNotice(
        `Remove failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const saveBudget = useMutation({
    mutationFn: (budget: TeamBudget | undefined) =>
      updateTeam(team.id, { name: team.name, budget }),
    onSuccess: async () => {
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: queries.teams().queryKey });
    },
    onError: (error) =>
      setNotice(
        `Budget save failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  // submitBudget reads the draft: an empty amount clears the budget, and
  // anything else must be a positive dollar amount.
  const submitBudget = () => {
    if (!draftBudget.trim()) {
      saveBudget.mutate(undefined);
      return;
    }
    const usd = Number(draftBudget);
    if (!Number.isFinite(usd) || usd <= 0) {
      setNotice("The spend budget must be a positive amount.");
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
    <SidePanel title={team.name} onClose={onClose}>
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-1">
          <span className="text-xs font-medium text-text-2">Team ID</span>
          <span className="font-mono text-sm text-text-1">{team.id}</span>
          <span className="text-xs text-text-4">
            created {formatRelativeTime(team.created_at)}
          </span>
        </div>

        {notice && <p className="text-xs text-error">{notice}</p>}

        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-text-2">Spend budget</span>
          <span className="text-xs text-text-4">
            Bounds what every key attributed to this team spends together,
            across every account the team reaches. Empty leaves the team
            unmetered.
          </span>
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
    </SidePanel>
  );
}
