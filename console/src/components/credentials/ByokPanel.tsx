import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useState } from "react";

import { CredentialApplyModal } from "@/components/credentials/CredentialApplyModal";
import { Field, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import {
  deleteBYOKCredential,
  listBYOKCredentials,
  listProviderCatalog,
  putBYOKCredential,
  validateBYOKCredential,
} from "@/lib/api";
import { formatRelativeTime, providerLabel } from "@/lib/format";

// BYOK is a provider credential one account brings for itself. It is addressed
// by account, never by gateway API key: the account's credentials outlive any
// key it rotates, and a second key in the same account reaches the same set.
//
// The deployment-wide credential an operator applies is a different thing with
// a different owner, a different route, and its own panel on the provider
// screen. It is not BYOK, and this word appears on no screen but this one — the
// question both answer is whose provider account pays for the call, and a
// screen that shows two answers at once has taught nobody the difference.

export function byokQueryKey(accountId: string): string[] {
  return ["byok", accountId];
}

export function ByokPanel({ accountId }: { accountId: string }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [provider, setProvider] = useState("");
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );

  const say = (text: string, error = false) => setNotice({ text, error });
  const reload = () =>
    queryClient.invalidateQueries({ queryKey: byokQueryKey(accountId) });

  const credentials = useQuery({
    queryKey: byokQueryKey(accountId),
    queryFn: () => listBYOKCredentials(accountId),
    retry: false,
  });
  const catalog = useQuery({
    queryKey: ["provider-catalog"],
    queryFn: listProviderCatalog,
    staleTime: 5 * 60_000,
  });

  // The catalog names both halves of this panel: what a provider's credential
  // is made of, and what the provider is called. A stored record carries only
  // the id, so the list resolves the name the same way the add control does
  // rather than showing an id beside a display name.
  const nameOf = (id: string) =>
    providerLabel(id, catalog.data?.find((entry) => entry.id === id)?.name);
  const fields =
    catalog.data?.find((entry) => entry.id === provider)?.credential_fields ??
    [];

  const validate = useMutation({
    mutationFn: (target: string) => validateBYOKCredential(accountId, target),
    onSuccess: (result, target) => {
      const valid = result?.valid !== false;
      say(
        `${nameOf(target)} BYOK credential is ${valid ? "valid" : "invalid"}`,
        !valid,
      );
    },
    onError: (error) =>
      say(
        `Validation failed: ${error instanceof Error ? error.message : error}`,
        true,
      ),
  });

  const remove = useMutation({
    mutationFn: (target: string) => deleteBYOKCredential(accountId, target),
    onSuccess: async (_result, target) => {
      say(`${nameOf(target)} BYOK credential removed`);
      await reload();
    },
    onError: (error) =>
      say(`Remove failed: ${error instanceof Error ? error.message : error}`, true),
  });

  const stored = credentials.data ?? [];
  const unstored = (catalog.data ?? []).filter(
    (entry) => !stored.some((record) => record.provider === entry.id),
  );

  return (
    <section data-testid="byok-section" className="flex flex-col gap-3">
      <div>
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-sm font-medium text-text-2">BYOK credentials</h3>
          <RowAction onClick={() => setAdding(true)}>add credential…</RowAction>
        </div>
        <p className="mt-1 text-xs text-text-3">
          Provider credentials this account brings for itself, stored encrypted
          and never returned. They are separate from the deployment credential
          an operator applies on a provider screen, and from this account&rsquo;s
          gateway API keys.
        </p>
      </div>

      {notice && (
        <p className={`text-xs ${notice.error ? "text-error" : "text-success"}`}>
          {notice.text}
        </p>
      )}

      {credentials.isPending ? (
        <p className="text-sm text-text-3">Loading BYOK credentials…</p>
      ) : credentials.error ? (
        <p className="text-sm text-text-3">
          Failed to load BYOK credentials: {credentials.error.message}
        </p>
      ) : stored.length === 0 ? (
        <p className="text-sm text-text-3">
          This account brings no credentials of its own.
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          {stored.map((record) => (
            <li
              key={record.provider}
              className="flex items-center gap-2 rounded-sm border border-border-1 bg-bg-panel px-3 py-2"
            >
              <div className="flex min-w-0 flex-col">
                <span className="truncate text-sm text-text-1">
                  {nameOf(record.provider)}
                </span>
                <span className="text-xs text-text-4">
                  stored {formatRelativeTime(record.created_at)}
                  {record.last_used
                    ? ` · last used ${formatRelativeTime(record.last_used)}`
                    : ""}
                </span>
              </div>
              <div className="ml-auto flex items-center gap-1">
                <RowAction
                  onClick={() => validate.mutate(record.provider)}
                  disabled={validate.isPending}
                >
                  validate
                </RowAction>
                <button
                  type="button"
                  onClick={() => remove.mutate(record.provider)}
                  disabled={remove.isPending}
                  aria-label={`Remove the ${record.provider} BYOK credential`}
                  className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
                >
                  <Trash2 className="size-3.5" />
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {adding && (
        <CredentialApplyModal
          key={provider}
          title="Add a BYOK credential"
          description="Stored against this account, encrypted and never returned. Only this account's requests use it."
          fields={fields}
          applyLabel="Store credential"
          ready={provider !== ""}
          apply={async (body) => {
            await putBYOKCredential(accountId, provider, body);
            await reload();
          }}
          validate={() => validateBYOKCredential(accountId, provider)}
          onClose={() => {
            setAdding(false);
            setProvider("");
          }}
        >
          <Field label="Provider">
            <Select
              value={provider}
              onChange={(event) => setProvider(event.target.value)}
              aria-label="Provider"
            >
              <option value="">Select a provider…</option>
              {unstored.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {providerLabel(entry.id, entry.name)}
                </option>
              ))}
            </Select>
          </Field>
        </CredentialApplyModal>
      )}
    </section>
  );
}
