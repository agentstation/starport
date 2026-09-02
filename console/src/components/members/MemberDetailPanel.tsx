import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useState } from "react";

import { Field, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { RelativeTime } from "@/components/ui/RelativeTime";
import {
  createAccountGrant,
  deleteAccountGrant,
  type Member,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import { report } from "@/lib/mutations";

// MemberDetailPanel is the operator's view of one member: who the identity
// provider resolved, which accounts this member holds directly, and which
// accounts are reachable once every team grant folds in. The direct grants
// are editable here; the reachable list is the gateway's computed answer and
// is read-only on purpose — the way to change it is to change a grant.

export function MemberDetailPanel({
  member,
  onClose,
}: {
  member: Member;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [draftAccount, setDraftAccount] = useState("");

  const grants = useQuery({
    ...queries.memberGrants(member.id),
  });
  const reachable = useQuery({
    ...queries.reachableAccounts(member.id),
  });
  const accounts = useQuery({
    ...queries.accounts(),
  });

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queries.memberGrants(member.id).queryKey }),
      queryClient.invalidateQueries({
        queryKey: queries.reachableAccounts(member.id).queryKey,
      }),
    ]);
  };

  const grant = useMutation({
    mutationFn: (accountId: string) =>
      createAccountGrant({ account_id: accountId, user_id: member.id }),
    onSuccess: async () => {
      setDraftAccount("");
      await refresh();
    },
    onError: (error) =>
      report(`Grant failed: ${error instanceof Error ? error.message : error}`),
  });

  const revoke = useMutation({
    mutationFn: (accountId: string) =>
      deleteAccountGrant({ account_id: accountId, user_id: member.id }),
    onSuccess: refresh,
    onError: (error) =>
      report(`Remove failed: ${error instanceof Error ? error.message : error}`),
  });

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
          <SheetTitle>{member.display_name || member.subject}</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <div className="flex flex-col gap-5">
            <div className="flex flex-col gap-1">
              <span className="text-xs font-medium text-text-2">Subject</span>
              <span className="font-mono text-sm text-text-1">
                {member.subject}
              </span>
              {member.email && (
                <span className="text-sm text-text-2">{member.email}</span>
              )}
              <span className="text-xs text-text-4">
                resolved <RelativeTime iso={member.created_at} />
              </span>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">Direct grants</span>
              {grants.data?.length === 0 && (
                <span className="text-sm text-text-3">
                  No account is granted to this member directly.
                </span>
              )}
              <ul className="flex flex-col gap-1">
                {(grants.data ?? []).map((row) => (
                  <li
                    key={row.account_id}
                    data-testid="member-grant-row"
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
                <Field label="Grant an account">
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

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">
                Reachable accounts
              </span>
              {reachable.data?.length === 0 && (
                <span className="text-sm text-text-3">
                  This member reaches no account yet.
                </span>
              )}
              <ul className="flex flex-col gap-1">
                {(reachable.data ?? []).map((accountId) => (
                  <li
                    key={accountId}
                    data-testid="reachable-account-row"
                    className="rounded-sm border border-border-1 bg-bg-panel px-3 py-2 font-mono text-xs text-text-1"
                  >
                    {accountId}
                  </li>
                ))}
              </ul>
              <span className="text-xs text-text-4">
                Direct grants folded with the grants of every team this member is
                on — the same answer the gateway resolves for this member's
                session.
              </span>
            </div>
          </div>
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}
