import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useState } from "react";

import {
  CredentialFieldInputs,
  credentialBody,
  hasSecretValue,
} from "@/components/credentials/CredentialFields";
import {
  Field,
  PrimaryButton,
  RowAction,
} from "@/components/ui/Form";
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

export function byokQueryKey(tenantId: string): string[] {
  return ["byok", tenantId];
}

export function ByokPanel({ tenantId }: { tenantId: string }) {
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );

  const say = (text: string, error = false) => setNotice({ text, error });
  const reload = () =>
    queryClient.invalidateQueries({ queryKey: byokQueryKey(tenantId) });

  const credentials = useQuery({
    queryKey: byokQueryKey(tenantId),
    queryFn: () => listBYOKCredentials(tenantId),
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

  const apply = useMutation({
    mutationFn: () =>
      putBYOKCredential(tenantId, provider, credentialBody(fields, values)),
    onSuccess: async () => {
      say(`BYOK credential stored for ${nameOf(provider)}`);
      setValues({});
      setProvider("");
      await reload();
    },
    onError: (error) =>
      say(`Store failed: ${error instanceof Error ? error.message : error}`, true),
  });

  const validate = useMutation({
    mutationFn: (target: string) => validateBYOKCredential(tenantId, target),
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
    mutationFn: (target: string) => deleteBYOKCredential(tenantId, target),
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
        <h3 className="text-sm font-medium text-text-2">BYOK credentials</h3>
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

      <div className="flex flex-col gap-2 border-t border-border-1 pt-3">
        <Field label="Add a BYOK credential">
          <Select
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value);
              setValues({});
            }}
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
        {provider &&
          (fields.length === 0 ? (
            <p className="text-xs text-text-4">
              {nameOf(provider)} declares no credential contract, so
              there is nothing to store.
            </p>
          ) : (
            <>
              <CredentialFieldInputs
                fields={fields}
                values={values}
                onChange={(id, value) =>
                  setValues((previous) => ({ ...previous, [id]: value }))
                }
              />
              <PrimaryButton
                onClick={() => apply.mutate()}
                disabled={!hasSecretValue(fields, values) || apply.isPending}
              >
                Store credential
              </PrimaryButton>
            </>
          ))}
      </div>
    </section>
  );
}
