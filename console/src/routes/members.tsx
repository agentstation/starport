import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";

import { MemberDetailPanel } from "@/components/members/MemberDetailPanel";
import { RowAction } from "@/components/ui/Form";
import { accessMessage, ApiError, listMembers } from "@/lib/api";
import { formatRelativeTime } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// A member is a user the identity provider resolved for this gateway. The
// console never invents one — people arrive through the identity grant — so
// this page reads and governs: who is here, and which accounts each member
// reaches, directly or through a team.

export const Route = createFileRoute("/members")({
  component: MembersPage,
});

const MEMBERS_KEY = ["members"];

function MembersPage() {
  const access = useGatewayAccess();
  const [selected, setSelected] = useState<string | null>(null);

  const members = useQuery({
    queryKey: MEMBERS_KEY,
    queryFn: listMembers,
    enabled: access,
    retry: false,
  });

  const rows = members.data ?? [];

  let body: ReactNode;
  if (members.error) {
    body = (
      <p className="text-base text-text-3">
        {members.error instanceof ApiError && members.error.needsKey
          ? accessMessage(members.error, "admin")
          : `Failed to load members: ${members.error.message}`}
      </p>
    );
  } else if (members.isPending) {
    body = <p className="text-base text-text-3">Loading members…</p>;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        Nobody is here yet. Members appear when an identity provider resolves
        them for this gateway.
      </p>
    );
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th className="px-4 py-2.5">Subject</th>
              <th className="px-4 py-2.5">Name</th>
              <th className="px-4 py-2.5">Email</th>
              <th className="px-4 py-2.5">Resolved</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((member) => (
              <tr
                key={member.id}
                data-testid="member-row"
                className="border-b border-border-1 last:border-0"
              >
                <td className="px-4 py-2">
                  <button
                    type="button"
                    onClick={() => setSelected(member.id)}
                    className="font-mono text-xs text-accent-link transition-colors duration-150 ease-standard hover:underline"
                  >
                    {member.subject}
                  </button>
                </td>
                <td className="px-4 py-2 text-text-2">
                  {member.display_name || "—"}
                </td>
                <td className="px-4 py-2 text-text-2">{member.email || "—"}</td>
                <td className="px-4 py-2 text-xs text-text-3">
                  {formatRelativeTime(member.created_at)}
                </td>
                <td className="px-4 py-2">
                  <div className="flex items-center justify-end">
                    <RowAction onClick={() => setSelected(member.id)}>
                      open
                    </RowAction>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  const open = rows.find((member) => member.id === selected);

  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Members</h1>
        <p className="mt-1 text-sm text-text-3">
          The users an identity provider resolved for this gateway. Open one to
          grant an account directly, or grant through a team.
        </p>
      </div>
      {body}
      {open && (
        <MemberDetailPanel member={open} onClose={() => setSelected(null)} />
      )}
    </div>
  );
}
