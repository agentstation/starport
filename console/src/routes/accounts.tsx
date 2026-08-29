import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";

import { ByokPanel } from "@/components/credentials/ByokPanel";
import {
  Field,
  GhostButton,
  INPUT_CLASS,
  PrimaryButton,
  RowAction,
} from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Modal } from "@/components/ui/Modal";
import { SidePanel } from "@/components/ui/SidePanel";
import {
  accessMessage,
  ApiError,
  createAccount,
  CREDENTIAL_STRATEGY_LABELS,
  DEFAULT_ACCOUNT_ID,
  deleteAccount,
  listKeys,
  listAccounts,
  updateAccount,
  type CredentialStrategy,
  type GatewayKey,
  type Account,
  type AccountLimits,
} from "@/lib/api";
import {
  formatCount,
  formatNanoUSD,
  formatRelativeTime,
  formatWindow,
} from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// An account is the unit an operator governs. Keys belong to one, limits meter
// the sum across all of its keys, and the provider credentials it brings for
// itself — its BYOK — belong to it rather than to any key it holds. That is why
// BYOK is here and on no other screen: the deployment-wide credential an
// operator applies belongs to no account and is edited on the provider itself.
//
// "Account" is the one word, on screen and on the wire (`account_id`). It is
// deliberately not "tenant": a tenant connotes allocated compute, while this
// unit carries identity, limits, and credential policy.

export const Route = createFileRoute("/accounts")({
  component: AccountsPage,
});

const STRATEGIES = Object.keys(CREDENTIAL_STRATEGY_LABELS) as CredentialStrategy[];

const ACCOUNTS_KEY = ["accounts"];

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
      await queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
    },
    onError: (error) =>
      setStrategyError(error instanceof Error ? error.message : String(error)),
  });

  return (
    <SidePanel title={account.name || account.id} onClose={onClose}>
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

        <ByokPanel accountId={account.id} />
      </div>
    </SidePanel>
  );
}

// --- Create ---

function CreateAccountModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (account: Account) => void;
}) {
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [strategy, setStrategy] =
    useState<CredentialStrategy>("operator_first");
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      createAccount({
        id: id.trim(),
        name: name.trim() || undefined,
        credential_strategy: strategy,
      }),
    onSuccess: onCreated,
    onError: (problem) =>
      setError(problem instanceof Error ? problem.message : String(problem)),
  });

  return (
    <Modal
      title="New account"
      onClose={onClose}
      footer={
        <>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton
            onClick={() => create.mutate()}
            disabled={!id.trim() || create.isPending}
          >
            Create account
          </PrimaryButton>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field
          label="Account ID"
          hint="The identifier keys and BYOK credentials are addressed by. It cannot change later."
        >
          <input
            value={id}
            onChange={(event) => setId(event.target.value)}
            placeholder="acme"
            autoComplete="off"
            spellCheck={false}
            className={`${INPUT_CLASS} font-mono`}
          />
        </Field>
        <Field label="Name" hint="What a person calls this account. Optional.">
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Acme Corp"
            autoComplete="off"
            className={INPUT_CLASS}
          />
        </Field>
        <Field label="Credential strategy">
          <Select
            value={strategy}
            onChange={(event) =>
              setStrategy(event.target.value as CredentialStrategy)
            }
            aria-label="Credential strategy"
          >
            {STRATEGIES.map((option) => (
              <option key={option} value={option}>
                {CREDENTIAL_STRATEGY_LABELS[option]}
              </option>
            ))}
          </Select>
        </Field>
        {error && <p className="text-sm text-error">{error}</p>}
      </div>
    </Modal>
  );
}

// --- Page ---

function AccountsPage() {
  const access = useGatewayAccess();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );

  const accounts = useQuery({
    queryKey: ACCOUNTS_KEY,
    queryFn: listAccounts,
    enabled: access,
    retry: false,
  });
  const keys = useQuery({
    queryKey: ["keys"],
    queryFn: listKeys,
    enabled: access,
    retry: false,
  });

  const remove = useMutation({
    mutationFn: (accountId: string) => deleteAccount(accountId),
    onSuccess: async (_result, accountId) => {
      setNotice({ text: `Account ${accountId} deleted` });
      setSelected(null);
      await queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
    },
    onError: (error) =>
      setNotice({
        text: `Delete failed: ${error instanceof Error ? error.message : error}`,
        error: true,
      }),
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
    body = <p className="text-base text-text-3">Loading accounts…</p>;
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
        <PrimaryButton onClick={() => setCreating(true)}>
          <Plus className="size-4" />
          New account
        </PrimaryButton>
      </div>
      {notice && (
        <p className={`text-sm ${notice.error ? "text-error" : "text-success"}`}>
          {notice.text}
        </p>
      )}
      {body}
      {open && (
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
            setNotice({ text: `Account ${account.id} created` });
            await queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
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
