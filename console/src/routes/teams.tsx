import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Plus, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";

import { TeamDetailPanel } from "@/components/teams/TeamDetailPanel";
import { INPUT_CLASS, PrimaryButton, RowAction } from "@/components/ui/Form";
import { TableSkeleton } from "@/components/ui/skeleton";
import { RelativeTime } from "@/components/ui/RelativeTime";
import {
  accessMessage,
  ApiError,
  createTeam,
  deleteTeam,
} from "@/lib/api";
import { queries, settle } from "@/lib/queries";
import { optionalString } from "@/lib/search";
import { formatNanoUSD } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import { announce, report } from "@/lib/mutations";

// A team is the operator's grouping lever: grant an account to a team once
// and everyone on the roster reaches it, including whoever joins later.

// The open team lives in the address, so a reload or a shared link lands
// on the same panel.
type TeamsSearch = { selected?: string };

export const Route = createFileRoute("/teams")({
  component: TeamsPage,
  loader: ({ context }) => settle(context.queryClient.ensureQueryData(queries.teams())),
  validateSearch: (search: Record<string, unknown>): TeamsSearch => ({
    selected: optionalString(search.selected),
  }),
});

function TeamsPage() {
  const access = useGatewayAccess();
  const queryClient = useQueryClient();
  const { selected } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const setSelected = (teamId: string | null) =>
    void navigate({ search: { selected: teamId ?? undefined }, replace: true });
  const [draftName, setDraftName] = useState("");

  const teams = useQuery({
    ...queries.teams(),
    enabled: access,
  });

  const create = useMutation({
    mutationFn: (name: string) => createTeam(name),
    onSuccess: async (team) => {
      setDraftName("");
      announce(`Team ${team.name} created`);
      await queryClient.invalidateQueries({ queryKey: queries.teams().queryKey });
      setSelected(team.id);
    },
    onError: (error) =>
      report(`Create failed: ${error instanceof Error ? error.message : error}`),
  });

  const remove = useMutation({
    mutationFn: (teamId: string) => deleteTeam(teamId),
    onSuccess: async (_result, teamId) => {
      announce("Team deleted");
      if (selected === teamId) setSelected(null);
      await queryClient.invalidateQueries({ queryKey: queries.teams().queryKey });
    },
    onError: (error) =>
      report(`Delete failed: ${error instanceof Error ? error.message : error}`),
  });

  const rows = teams.data ?? [];

  let body: ReactNode;
  if (teams.error) {
    body = (
      <p className="text-base text-text-3">
        {teams.error instanceof ApiError && teams.error.needsKey
          ? accessMessage(teams.error, "admin")
          : `Failed to load teams: ${teams.error.message}`}
      </p>
    );
  } else if (teams.isPending) {
    body = <TableSkeleton columns={4} />;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        No team yet. Name one above to grant accounts to a group instead of one
        member at a time.
      </p>
    );
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th scope="col" className="px-4 py-2.5">Team</th>
              <th scope="col" className="px-4 py-2.5 text-right">Spend budget</th>
              <th scope="col" className="px-4 py-2.5">Created</th>
              <th scope="col" className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((team) => (
              <tr
                key={team.id}
                data-testid="team-row"
                className="border-b border-border-1 last:border-0"
              >
                <td className="px-4 py-2.5">
                  <button
                    type="button"
                    onClick={() => setSelected(team.id)}
                    className="text-sm text-accent-link transition-colors duration-150 ease-standard hover:underline"
                  >
                    {team.name}
                  </button>
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums text-xs text-text-3">
                  {team.budget
                    ? `${formatNanoUSD(team.budget.limit)} / ${team.budget.interval}`
                    : "—"}
                </td>
                <td className="px-4 py-2.5 text-xs text-text-3">
                  <RelativeTime iso={team.created_at} />
                </td>
                <td className="px-4 py-2.5">
                  <div className="flex items-center justify-end gap-1">
                    <RowAction onClick={() => setSelected(team.id)}>
                      open
                    </RowAction>
                    <button
                      type="button"
                      onClick={() => remove.mutate(team.id)}
                      disabled={remove.isPending}
                      aria-label={`Delete the ${team.name} team`}
                      className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  const open = rows.find((team) => team.id === selected);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.01em]">Teams</h1>
          <p className="mt-1 text-sm text-text-3">
            Grant an account to a team once and everyone on its roster reaches
            it, including whoever joins later.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <input
            value={draftName}
            onChange={(event) => setDraftName(event.target.value)}
            placeholder="Team name"
            aria-label="Team name"
            className={INPUT_CLASS}
          />
          <PrimaryButton
            onClick={() => draftName.trim() && create.mutate(draftName.trim())}
            disabled={!draftName.trim() || create.isPending}
          >
            <Plus className="size-4" />
            New team
          </PrimaryButton>
        </div>
      </div>
      {body}
      {open && <TeamDetailPanel team={open} onClose={() => setSelected(null)} />}
    </div>
  );
}
