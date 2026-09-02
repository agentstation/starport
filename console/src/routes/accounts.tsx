import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { LayoutTemplate, Plus, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";

import { AccountPolicyPanel } from "@/components/accounts/AccountPolicyPanel";
import { AccountTemplatesPanel } from "@/components/accounts/AccountTemplatesPanel";
import { CreateAccountModal } from "@/components/accounts/CreateAccountModal";
import { ByokPanel } from "@/components/credentials/ByokPanel";
import { Field, GhostButton, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { TableSkeleton } from "@/components/ui/skeleton";
import {
  accessMessage,
  ApiError,
  CREDENTIAL_STRATEGY_LABELS,
  DEFAULT_ACCOUNT_ID,
  deleteAccount,
  updateAccount,
  type CredentialStrategy,
  type GatewayKey,
  type Account,
  type AccountLimits,
} from "@/lib/api";
import { queries, settle } from "@/lib/queries";
import { oneOf, optionalString } from "@/lib/search";
import {
  formatCount,
  formatNanoUSD,
  formatRelativeTime,
  formatWindow,
} from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import { announce, report } from "@/lib/mutations";

// An account is the unit an operator governs. Keys belong to one, limits meter
// the sum across all of its keys, and the provider credentials it brings for
// itself — its BYOK — belong to it rather than to any key it holds. That is why
// BYOK is here and on no other screen: the deployment-wide credential an
// operator applies belongs to no account and is edited on the provider itself.
//
// "Account" is the one word, on screen and on the wire (`account_id`). It is
// deliberately not "tenant": a tenant connotes allocated compute, while this
// unit carries identity, limits, and credential policy.

// The open account and the open panel live in the address, so a reload
// or a shared link lands on the same view.
const PANELS = ["create", "templates"] as const;
type AccountsSearch = { selected?: string; panel?: (typeof PANELS)[number] };

export const Route = createFileRoute("/accounts")({
  component: AccountsPage,
  loader: ({ context }) =>
    settle(
      context.queryClient.ensureQueryData(queries.accounts()),
      context.queryClient.ensureQueryData(queries.keys()),
    ),
  validateSearch: (search: Record<string, unknown>): AccountsSearch => ({
    selected: optionalString(search.selected),
    panel: oneOf(PANELS, search.panel),
  }),
});

const STRATEGIES = Object.keys(CREDENTIAL_STRATEGY_LABELS) as CredentialStrategy[];

// LimitChips states the account ceiling. It is not a key's ceiling: a request
// from any key in the account counts against these, and a key with its own
// limit satisfies both.
function LimitChips({ limits }: { limits: AccountLimits | null | undefined }) {
  const chips: string[] = [];
  if (limits?.requests?.limit) {
    chips.push(
      `${formatCount(limits.requests.limit)} req/${formatWindow(limits.requests.window_seconds)}`,
    );
  }
  if (limits?.spend?.limit) {
    chips.push(`${formatNanoUSD(limits.spend.limit)}/${limits.spend.interval}`);
  }
  if (limits?.tokens?.limit) {
    chips.push(
      `${formatCount(limits.tokens.limit)} tok/${limits.tokens.interval}`,
    );
  }
  if (chips.length === 0) return <span className="text-text-4">no ceiling</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {chips.map((chip) => (
        <span
          key={chip}
          className="inline-flex h-5 items-center whitespace-nowrap rounded-xs bg-bg-raised px-1.5 text-xs text-text-3"
        >
          {chip}
        </span>
      ))}
    </div>
  );
}

// --- Account detail ---

function AccountDetail({
  account,
  keys,
  onClose,
}: {
  account: Account;
  keys: GatewayKey[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [strategyError, setStrategyError] = useState<string | null>(null);

  const strategy = useMutation({
    mutationFn: (next: CredentialStrategy) =>
      updateAccount(account.id, { credential_strategy: next }),
    onSuccess: async () => {
      setStrategyError(null);
      await queryClient.invalidateQueries({ queryKey: queries.accounts().queryKey });
    },
    onError: (error) =>
      setStrategyError(error instanceof Error ? error.message : String(error)),
  });

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{account.name || account.id}</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <div className="flex flex-col gap-5">
            <div className="flex flex-col gap-1">
              <span className="text-xs font-medium text-text-2">Account ID</span>
              <span className="font-mono text-sm text-text-1">{account.id}</span>
              <span className="text-xs text-text-4">
                created {formatRelativeTime(account.created_at)}
              </span>
            </div>

            <Field
              label="Credential strategy"
              hint="Which credentials serve this account, and in which order."
            >
              <Select
                value={account.credential_strategy ?? "operator_first"}
                onChange={(event) =>
                  strategy.mutate(event.target.value as CredentialStrategy)
                }
                disabled={strategy.isPending}
                aria-label="Credential strategy"
              >
                {STRATEGIES.map((option) => (
                  <option key={option} value={option}>
                    {CREDENTIAL_STRATEGY_LABELS[option]}
                  </option>
                ))}
              </Select>
            </Field>
            {strategyError && (
              <p className="text-xs text-error">Update failed: {strategyError}</p>
            )}

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">Account limits</span>
              <LimitChips limits={account.limits} />
              <span className="text-xs text-text-4">
                Metered across every key in the account. A key with its own limit
                satisfies both.
              </span>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-text-2">
                Gateway API keys ({formatCount(keys.length)})
              </span>
              {keys.length === 0 ? (
                <span className="text-sm text-text-3">
                  No key names this account yet.
                </span>
              ) : (
                <ul className="flex flex-col gap-1">
                  {keys.map((apiKey) => (
                    <li
                      key={apiKey.id}
                      className="flex items-center gap-2 rounded-sm border border-border-1 bg-bg-panel px-3 py-2 text-sm"
                    >
                      <span className="truncate text-text-1">
                        {apiKey.name || apiKey.id}
                      </span>
                      {apiKey.active === false && (
                        <span className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3">
                          disabled
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              <Link
                to="/keys"
                className="text-xs text-accent-link transition-colors duration-150 ease-standard hover:underline"
              >
                Manage keys →
              </Link>
            </div>

            {/* The key remounts the policy drafts when the operator opens a
                different account in the same panel position. */}
            <AccountPolicyPanel key={account.id} account={account} />

            <ByokPanel accountId={account.id} />
          </div>
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}

// --- Page ---

function AccountsPage() {
  const access = useGatewayAccess();
  const queryClient = useQueryClient();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const setSearch = (patch: Partial<AccountsSearch>) =>
    void navigate({
      search: (previous: AccountsSearch) => ({ ...previous, ...patch }),
      replace: true,
    });
  const selected = search.selected ?? null;
  const creating = search.panel === "create";
  const managingTemplates = search.panel === "templates";
  const setSelected = (accountId: string | null) =>
    setSearch({ selected: accountId ?? undefined });
  const setCreating = (open: boolean) => setSearch({ panel: open ? "create" : undefined });
  const setManagingTemplates = (open: boolean) =>
    setSearch({ panel: open ? "templates" : undefined });

  const accounts = useQuery({
    ...queries.accounts(),
    enabled: access,
  });
  const keys = useQuery({
    ...queries.keys(),
    enabled: access,
  });

  const remove = useMutation({
    mutationFn: (accountId: string) => deleteAccount(accountId),
    onSuccess: async (_result, accountId) => {
      announce(`Account ${accountId} deleted`);
      setSelected(null);
      await queryClient.invalidateQueries({ queryKey: queries.accounts().queryKey });
    },
    onError: (error) =>
      report(`Delete failed: ${error instanceof Error ? error.message : error}`),
  });

  const rows = accounts.data ?? [];
  const keysFor = (accountId: string) =>
    (keys.data ?? []).filter(
      (apiKey) => (apiKey.account_id || DEFAULT_ACCOUNT_ID) === accountId,
    );

  let body: ReactNode;
  if (accounts.error) {
    body = (
      <p className="text-base text-text-3">
        {accounts.error instanceof ApiError && accounts.error.needsKey
          ? accessMessage(accounts.error, "admin")
          : `Failed to load accounts: ${accounts.error.message}`}
      </p>
    );
  } else if (accounts.isPending) {
    body = <TableSkeleton columns={6} />;
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th className="px-4 py-2.5">Account</th>
              <th className="px-4 py-2.5">Name</th>
              <th className="px-4 py-2.5">Credentials</th>
              <th className="px-4 py-2.5">Limits</th>
              <th className="px-4 py-2.5">Keys</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((account) => (
              <tr
                key={account.id}
                data-testid="account-row"
                className="border-b border-border-1 last:border-0"
              >
                <td className="px-4 py-2">
                  <button
                    type="button"
                    onClick={() => setSelected(account.id)}
                    className="font-mono text-xs text-accent-link transition-colors duration-150 ease-standard hover:underline"
                  >
                    {account.id}
                  </button>
                  {account.id === DEFAULT_ACCOUNT_ID && (
                    <span className="ml-2 inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3">
                      canonical
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 text-text-2">{account.name || "—"}</td>
                <td className="px-4 py-2 text-xs text-text-3">
                  {
                    CREDENTIAL_STRATEGY_LABELS[
                      account.credential_strategy ?? "operator_first"
                    ]
                  }
                </td>
                <td className="px-4 py-2">
                  <LimitChips limits={account.limits} />
                </td>
                <td className="px-4 py-2 tabular-nums text-text-2">
                  {formatCount(keysFor(account.id).length)}
                </td>
                <td className="px-4 py-2">
                  <div className="flex items-center justify-end gap-1">
                    <RowAction onClick={() => setSelected(account.id)}>
                      open
                    </RowAction>
                    {account.id !== DEFAULT_ACCOUNT_ID && (
                      <button
                        type="button"
                        onClick={() => remove.mutate(account.id)}
                        disabled={remove.isPending}
                        aria-label={`Delete the ${account.id} account`}
                        className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
                      >
                        <Trash2 className="size-3.5" />
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  const open = rows.find((account) => account.id === selected);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <Header />
        <div className="flex items-center gap-2">
          <GhostButton
            onClick={() => setSearch({ selected: undefined, panel: "templates" })}
          >
            <LayoutTemplate className="size-4" />
            Templates
          </GhostButton>
          <PrimaryButton onClick={() => setCreating(true)}>
            <Plus className="size-4" />
            New account
          </PrimaryButton>
        </div>
      </div>
      {body}
      {managingTemplates && (
        <AccountTemplatesPanel onClose={() => setManagingTemplates(false)} />
      )}
      {!managingTemplates && open && (
        <AccountDetail
          account={open}
          keys={keysFor(open.id)}
          onClose={() => setSelected(null)}
        />
      )}
      {creating && (
        <CreateAccountModal
          onClose={() => setCreating(false)}
          onCreated={async (account) => {
            setCreating(false);
            announce(`Account ${account.id} created`);
            await queryClient.invalidateQueries({ queryKey: queries.accounts().queryKey });
            setSelected(account.id);
          }}
        />
      )}
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold tracking-[-0.01em]">Accounts</h1>
      <p className="mt-1 text-sm text-text-3">
        The accounts this gateway governs. An account owns its keys, its
        spending ceiling, and the provider credentials it brings for itself.
      </p>
    </div>
  );
}
