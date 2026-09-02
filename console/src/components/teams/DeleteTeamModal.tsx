import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { ConfirmDialog, reasonOf } from "@/components/ui/ConfirmDialog";
import { deleteTeam, type Team } from "@/lib/api";
import { formatCount } from "@/lib/format";
import { queries } from "@/lib/queries";

// DeleteTeamModal restates what a team delete reaches before it travels. The
// gateway removes the roster and the team's account grants with the team, so
// the dialog names both counts from the same reads the detail panel uses.
export function DeleteTeamModal({
  team,
  onClose,
  onDeleted,
}: {
  team: Team;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [error, setError] = useState("");
  const roster = useQuery(queries.teamMembers(team.id));
  const grants = useQuery(queries.teamGrants(team.id));
  const remove = useMutation({
    mutationFn: () => deleteTeam(team.id),
    onSuccess: onDeleted,
    onError: (problem) => setError(`Delete failed: ${reasonOf(problem)}`),
  });
  return (
    <ConfirmDialog
      title="Delete team"
      action="Delete team"
      error={error}
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
      onClose={onClose}
    >
      <p>
        Delete <strong className="text-text-1">{team.name}</strong>?{" "}
        {plural(roster.data?.length, "member", "Every member")} leave the roster, and{" "}
        {plural(grants.data?.length, "account grant", "every account grant").toLowerCase()}{" "}
        end with the team. Keys attributed to the team spend without a team
        ceiling from the next request on.
      </p>
    </ConfirmDialog>
  );
}

// plural names a count once it is read, and falls back to the whole while the
// read is in flight, so the sentence never claims a number it has not seen.
function plural(count: number | undefined, noun: string, whole: string): string {
  if (count === undefined) return whole;
  return `Its ${formatCount(count)} ${noun}${count === 1 ? "" : "s"}`;
}
