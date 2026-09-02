import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useRef, useState } from "react";

import { CredentialApplyModal } from "@/components/credentials/CredentialApplyModal";
import { SourcePill } from "@/components/credentials/SourcePill";
import { DestructiveButton, Field, GhostButton, INPUT_CLASS, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Dialog, DialogBody, DialogContent, DialogError, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { LoadingStatus, Skeleton } from "@/components/ui/skeleton";
import {
  ApiError,
  createSharedCredential,
  deleteSharedCredential,
  updateSharedCredential,
  validateSharedCredential,
  type CredentialField,
  type SharedCredentialSummary,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import { formatCount, formatRelativeTime } from "@/lib/format";
import { announce, errorText, report } from "@/lib/mutations";

// A shared credential is a provider credential an operator shares with the
// deployment's accounts. It belongs to no account, so it is addressed by
// provider and the id the gateway assigned it, and edited here, on the
// provider's own screen, rather than anywhere near a gateway API key. On
// screen the word is "shared" — the stored half of the Shared group, beside
// the environment credential that is the same operator's money — and the
// keyring and the wire say "shared" too (internal/providers/keyring).
//
// A provider can hold several shared credentials, and this panel shows the
// whole plane: each credential with its label and its access rule — open to
// every account, or granted to a listed few. The access question is asked at
// creation and defaults to every account; the grants stay editable per
// credential afterward. A request uses the first usable credential in list
// order, so the first row wears the Active pill when no earlier source (the
// environment credential) shadows the plane.
//
// It is not BYOK. BYOK is a credential an account brings for itself, and it
// is managed per account. The provider credential drawer may name accounts as
// the third resolution source, but this panel edits no account credential:
// everything it stores belongs to the deployment. The section renders as a
// row of that drawer and carries no chrome of its own.

type Access = "open" | "granted";

// AccessChoice is the access question, asked at creation and re-asked when
// the operator edits a credential's grants: every account, or only granted
// accounts — with open as the default, because the shared plane's promise is
// that one stored credential serves the deployment unless the operator
// narrows it.
function AccessChoice({
  access,
  grants,
  onChange,
}: {
  access: Access;
  grants: string[];
  onChange: (access: Access, grants: string[]) => void;
}) {
  const accounts = useQuery({
    ...queries.accounts(),
  });

  const toggleGrant = (accountId: string) => {
    const next = grants.includes(accountId)
      ? grants.filter((grant) => grant !== accountId)
      : [...grants, accountId];
    onChange("granted", next);
  };

  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="text-xs font-medium text-text-2">
        Which accounts can use it?
      </legend>
      <label className="flex items-start gap-2 text-sm text-text-2">
        <input
          type="radio"
          name="shared-access"
          checked={access === "open"}
          onChange={() => onChange("open", [])}
          className="mt-1"
        />
        <span>
          Every account
          <span className="block text-xs text-text-4">
            Any account&rsquo;s requests can use this credential.
          </span>
        </span>
      </label>
      <label className="flex items-start gap-2 text-sm text-text-2">
        <input
          type="radio"
          name="shared-access"
          checked={access === "granted"}
          onChange={() => onChange("granted", grants)}
          className="mt-1"
        />
        <span>
          Only granted accounts
          <span className="block text-xs text-text-4">
            Only the accounts chosen below can use it; everyone else resolves
            past it.
          </span>
        </span>
      </label>
      {access === "granted" &&
        (accounts.error ? (
          <p className="pl-6 text-xs text-text-4">
            Could not list accounts:{" "}
            {accounts.error instanceof Error
              ? accounts.error.message
              : String(accounts.error)}
          </p>
        ) : (
          <div className="flex flex-col gap-1 pl-6">
            {(accounts.data ?? []).map((account) => (
              <label
                key={account.id}
                className="flex items-center gap-2 text-sm text-text-2"
              >
                <input
                  type="checkbox"
                  checked={grants.includes(account.id)}
                  onChange={() => toggleGrant(account.id)}
                />
                {account.name || account.id}
              </label>
            ))}
            {accounts.data?.length === 0 && (
              <p className="text-xs text-text-4">
                No accounts exist yet to grant.
              </p>
            )}
          </div>
        ))}
    </fieldset>
  );
}

// accessWords is the row's one-line answer to who a credential serves. The
// vocabulary matches the create flow's question, so the list teaches the
// same words it asks with.
function accessWords(credential: SharedCredentialSummary): string {
  if (credential.access === "granted") {
    const grants = credential.grants ?? [];
    return grants.length > 0
      ? `Only granted accounts: ${grants.join(", ")}`
      : "Only granted accounts — none granted yet";
  }
  return "Every account";
}

export function SharedCredentialPanel({
  providerId,
  name,
  fields,
  // Whether requests would use this source: true when no earlier source
  // (the environment credential) is usable. A stored credential behind a
  // usable environment credential is stored but shadowed, and the first
  // row's pill says Applied rather than Active.
  active,
}: {
  providerId: string;
  name: string;
  fields: CredentialField[];
  active: boolean;
}) {
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const [createLabel, setCreateLabel] = useState("");
  const [createAccess, setCreateAccess] = useState<Access>("open");
  const [createGrants, setCreateGrants] = useState<string[]>([]);
  const [replacing, setReplacing] = useState<SharedCredentialSummary | null>(
    null,
  );
  const [editingAccess, setEditingAccess] =
    useState<SharedCredentialSummary | null>(null);
  const [removing, setRemoving] = useState<SharedCredentialSummary | null>(
    null,
  );
  const [removeError, setRemoveError] = useState("");
  // The id the apply modal's validate call addresses. A replace knows it up
  // front; a create learns it from the response, after apply and before
  // validate, so a ref carries it across the two calls.
  const applyTarget = useRef<string | null>(null);

  // A stored credential changes what the provider can serve, so the status
  // read refreshes with the list.
  const refresh = () =>
    Promise.all([
      queryClient.invalidateQueries({
        queryKey: queries.sharedCredentials(providerId).queryKey,
      }),
      queryClient.invalidateQueries({ queryKey: queries.providerStatus().queryKey }),
    ]);

  const credentials = useQuery({
    ...queries.sharedCredentials(providerId),
  });

  const validate = useMutation({
    mutationFn: (credential: SharedCredentialSummary) =>
      validateSharedCredential(providerId, credential.id),
    onSuccess: (result, credential) => {
      const valid = result?.valid !== false;
      const who = credential.label || "The shared credential";
      if (valid) announce(`${who} is valid`);
      else report(`${who} is invalid`);
    },
    onError: (error) =>
      report(`Validation failed: ${error instanceof Error ? error.message : error}`),
  });

  const remove = useMutation({
    mutationFn: (credential: SharedCredentialSummary) =>
      deleteSharedCredential(providerId, credential.id),
    onSuccess: async () => {
      setRemoving(null);
      announce("Shared credential removed");
      await refresh();
    },
    onError: (error) =>
      setRemoveError(
        `Remove failed: ${error instanceof Error ? error.message : error}`,
      ),
  });

  const locked =
    credentials.error instanceof ApiError && credentials.error.needsKey;
  const stored = credentials.data ?? [];
  const applied = stored.length > 0;
  const missing = credentials.data !== undefined && !applied;

  const openCreate = () => {
    setCreateLabel("");
    setCreateAccess("open");
    setCreateGrants([]);
    setCreating(true);
  };

  return (
    <section
      data-testid="shared-credential-panel"
      className="flex min-w-0 flex-1 flex-col gap-2"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-medium text-text-1">Stored</h3>
        {missing && <SourcePill label="Not set" tone="neutral" />}
        {applied && (
          <div className="ml-auto">
            <RowAction onClick={openCreate}>add credential…</RowAction>
          </div>
        )}
      </div>


      {credentials.isPending ? (
        <LoadingStatus className="flex flex-col gap-2">
          <Skeleton className="h-9" />
          <Skeleton className="h-9" />
        </LoadingStatus>
      ) : locked ? (
        <p className="text-sm text-text-3">
          Applied by an operator for the whole deployment. Only an operator key
          with the admin scope can read or apply one.
        </p>
      ) : credentials.error ? (
        <p className="text-sm text-text-3">
          Failed to load the credentials: {credentials.error.message}
        </p>
      ) : applied ? (
        <ul className="flex flex-col gap-2">
          {stored.map((credential, index) => (
            <li
              key={credential.id}
              className="flex flex-col gap-1 rounded-xs border border-border-1 p-2"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium text-text-1">
                  {credential.label || "Unlabeled"}
                </span>
                {index === 0 && (
                  <SourcePill
                    label={active ? "Active" : "Applied"}
                    tone={active ? "success" : "info"}
                    title={
                      active
                        ? "Requests use this credential first"
                        : "Stored, but the environment credential is used first"
                    }
                  />
                )}
                <div className="ml-auto flex items-center gap-1">
                  <RowAction
                    onClick={() => validate.mutate(credential)}
                    disabled={validate.isPending}
                  >
                    validate
                  </RowAction>
                  <RowAction
                    onClick={() => {
                      applyTarget.current = credential.id;
                      setReplacing(credential);
                    }}
                  >
                    replace
                  </RowAction>
                  <RowAction onClick={() => setEditingAccess(credential)}>
                    access…
                  </RowAction>
                  <button
                    type="button"
                    onClick={() => {
                      setRemoveError("");
                      setRemoving(credential);
                    }}
                    aria-label={`Remove the ${credential.label || providerId} shared credential`}
                    className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                </div>
              </div>
              <p className="text-xs text-text-3">{accessWords(credential)}</p>
              <p className="text-xs text-text-4">
                Stored encrypted and never returned
                {credential.created_at
                  ? ` · applied ${formatRelativeTime(credential.created_at)}`
                  : ""}
                {credential.last_used
                  ? ` · last used ${formatRelativeTime(credential.last_used)}`
                  : ""}
                {typeof credential.usage_count === "number"
                  ? ` · ${formatCount(credential.usage_count)} requests`
                  : ""}
                .
              </p>
            </li>
          ))}
        </ul>
      ) : (
        <>
          <p className="text-sm text-text-2">
            No shared credential is stored for this provider. One stored here
            pays the deployment&rsquo;s requests — every account by default,
            or only the accounts you grant — encrypted and never returned.
          </p>
          <div>
            <PrimaryButton onClick={openCreate}>Set credential</PrimaryButton>
          </div>
        </>
      )}

      {creating && (
        <CredentialApplyModal
          title={applied ? "Add shared credential" : "Set shared credential"}
          description={`A shared credential for ${name}, stored encrypted and never returned.`}
          fields={fields}
          apply={async (body) => {
            const created = await createSharedCredential(providerId, {
              ...body,
              label: createLabel.trim() || undefined,
              access: createAccess,
              grants: createAccess === "granted" ? createGrants : undefined,
            });
            applyTarget.current = created.id;
            await refresh();
          }}
          validate={() => {
            const credentialId = applyTarget.current;
            if (!credentialId) {
              return Promise.reject(new Error("no credential was applied"));
            }
            return validateSharedCredential(providerId, credentialId);
          }}
          onClose={() => setCreating(false)}
        >
          <Field label="Label" hint="How the list and the payer line name it.">
            <input
              className={INPUT_CLASS}
              value={createLabel}
              onChange={(event) => setCreateLabel(event.target.value)}
              placeholder="e.g. team-a"
              aria-label="Label"
            />
          </Field>
          <AccessChoice
            access={createAccess}
            grants={createGrants}
            onChange={(access, grants) => {
              setCreateAccess(access);
              setCreateGrants(grants);
            }}
          />
        </CredentialApplyModal>
      )}

      {replacing && (
        <CredentialApplyModal
          title="Replace shared credential"
          description={`A new value for ${replacing.label || "this credential"}. Its grants and usage history stay; only the stored secret changes.`}
          fields={fields}
          apply={async (body) => {
            await updateSharedCredential(providerId, replacing.id, body);
            await refresh();
          }}
          validate={() => validateSharedCredential(providerId, replacing.id)}
          onClose={() => setReplacing(null)}
        />
      )}

      {editingAccess && (
        <AccessEditModal
          providerId={providerId}
          credential={editingAccess}
          onSaved={async () => {
            setEditingAccess(null);
            announce("Access updated");
            await refresh();
          }}
          onClose={() => setEditingAccess(null)}
        />
      )}

      {removing && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setRemoving(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Remove shared credential</DialogTitle>
            </DialogHeader>
            <DialogBody>
              <p className="text-sm text-text-2">
                This removes{" "}
                <strong className="font-semibold text-text-1">
                  {removing.label || "the shared credential"}
                </strong>{" "}
                for {name}. Requests stop using it immediately; accounts fall back
                to the remaining shared credentials, the environment credential,
                or their own. The stored value cannot be recovered.
              </p>
            </DialogBody>
            <DialogError>{removeError}</DialogError>
            <DialogFooter>
              <GhostButton onClick={() => setRemoving(null)}>
                Cancel
              </GhostButton>
              <DestructiveButton
                onClick={() => remove.mutate(removing)}
                disabled={remove.isPending}
              >
                Remove
              </DestructiveButton>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </section>
  );
}

// AccessEditModal re-asks the access question for one stored credential. The
// answer replaces the whole rule — the access word and the grant list
// together — so what the operator sees checked is exactly what the gateway
// enforces after Save.
function AccessEditModal({
  providerId,
  credential,
  onSaved,
  onClose,
}: {
  providerId: string;
  credential: SharedCredentialSummary;
  onSaved: () => Promise<void>;
  onClose: () => void;
}) {
  const [error, setError] = useState("");
  const [access, setAccess] = useState<Access>(
    credential.access === "granted" ? "granted" : "open",
  );
  const [grants, setGrants] = useState<string[]>(credential.grants ?? []);

  const save = useMutation({
    mutationFn: () =>
      updateSharedCredential(providerId, credential.id, {
        access,
        grants: access === "granted" ? grants : [],
      }),
    onMutate: () => setError(""),
    onSuccess: () => onSaved(),
    onError: (problem) => setError(`Access update failed: ${errorText(problem)}`),
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
          <DialogTitle>{`Access for ${credential.label || "the shared credential"}`}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <AccessChoice
            access={access}
            grants={grants}
            onChange={(nextAccess, nextGrants) => {
              setAccess(nextAccess);
              setGrants(nextGrants);
            }}
          />
        </DialogBody>
        <DialogError>{error}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose} disabled={save.isPending}>
            Cancel
          </GhostButton>
          <PrimaryButton onClick={() => save.mutate()} disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save access"}
          </PrimaryButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
