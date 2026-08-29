import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useRef, useState } from "react";

import { CredentialApplyModal } from "@/components/credentials/CredentialApplyModal";
import { SourcePill } from "@/components/credentials/SourcePill";
import { GhostButton, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Modal } from "@/components/ui/Modal";
import {
  ApiError,
  createSharedCredential,
  deleteSharedCredential,
  listSharedCredentials,
  updateSharedCredential,
  validateSharedCredential,
  type CredentialField,
} from "@/lib/api";
import { formatCount, formatRelativeTime } from "@/lib/format";

// A shared credential is a provider credential an operator shares with the
// deployment's accounts. It belongs to no account, so it is addressed by
// provider and the id the gateway assigned it, and edited here, on the
// provider's own screen, rather than anywhere near a gateway API key. On
// screen the word is "shared" — the stored half of the Shared group, beside
// the environment credential that is the same operator's money — and the
// keyring and the wire say "shared" too (internal/providers/keyring).
//
// A provider can hold several shared credentials. This panel still renders
// the plane as one row — the first credential drives it — until the list UI
// lands; the wire underneath is already the list.
//
// It is not BYOK. BYOK is a credential an account brings for itself, and it is
// managed per account. The provider credential drawer may name accounts as the
// third resolution source, but this panel never edits one and never names an
// account: the credentials it applies belong to the deployment. The section
// renders as a row of that drawer and carries no chrome of its own.

export function sharedCredentialsQueryKey(providerId: string): string[] {
  return ["shared-credentials", providerId];
}

export function SharedCredentialPanel({
  providerId,
  name,
  fields,
  // Whether requests would use this source: true when no earlier source
  // (the environment credential) is usable. An applied credential behind a
  // usable environment credential is stored but shadowed, and its pill says
  // Applied rather than Active.
  active,
}: {
  providerId: string;
  name: string;
  fields: CredentialField[];
  active: boolean;
}) {
  const queryClient = useQueryClient();
  const [setting, setSetting] = useState(false);
  const [confirmingRemove, setConfirmingRemove] = useState(false);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );
  // The id the apply modal's validate call addresses. A replace knows it up
  // front; a create learns it from the response, after apply and before
  // validate, so a ref carries it across the two calls.
  const applyTarget = useRef<string | null>(null);

  const say = (text: string, error = false) => setNotice({ text, error });
  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: sharedCredentialsQueryKey(providerId),
    });

  const credentials = useQuery({
    queryKey: sharedCredentialsQueryKey(providerId),
    queryFn: () => listSharedCredentials(providerId),
    retry: false,
  });

  const stored = credentials.data?.[0];

  const validate = useMutation({
    mutationFn: (credentialId: string) =>
      validateSharedCredential(providerId, credentialId),
    onSuccess: (result) => {
      const valid = result?.valid !== false;
      say(
        valid ? "Shared credential is valid" : "Shared credential is invalid",
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
    mutationFn: (credentialId: string) =>
      deleteSharedCredential(providerId, credentialId),
    onSuccess: async () => {
      setConfirmingRemove(false);
      say("Shared credential removed");
      await refresh();
    },
    onError: (error) => {
      setConfirmingRemove(false);
      say(`Remove failed: ${error instanceof Error ? error.message : error}`, true);
    },
  });

  const locked =
    credentials.error instanceof ApiError && credentials.error.needsKey;
  const applied = stored !== undefined;
  const missing = credentials.data !== undefined && !applied;

  return (
    <section
      data-testid="gateway-credential-panel"
      className="flex min-w-0 flex-1 flex-col gap-2"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-medium text-text-1">Stored</h3>
        {applied && (
          <SourcePill
            label={active ? "Active" : "Applied"}
            tone={active ? "success" : "info"}
            title={
              active
                ? "Requests use this credential"
                : "Stored, but the environment credential is used first"
            }
          />
        )}
        {missing && <SourcePill label="Not set" tone="neutral" />}
        {applied && (
          <div className="ml-auto flex items-center gap-1">
            <RowAction
              onClick={() => validate.mutate(stored.id)}
              disabled={validate.isPending}
            >
              validate
            </RowAction>
            <RowAction onClick={() => setSetting(true)}>replace</RowAction>
            <button
              type="button"
              onClick={() => setConfirmingRemove(true)}
              aria-label={`Remove the ${providerId} shared credential`}
              className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
        )}
      </div>

      {notice && (
        <p className={`text-xs ${notice.error ? "text-error" : "text-success"}`}>
          {notice.text}
        </p>
      )}

      {credentials.isPending ? (
        <p className="text-sm text-text-3">Loading credential…</p>
      ) : locked ? (
        <p className="text-sm text-text-3">
          Applied by an operator for the whole deployment. Only an operator key
          with the admin scope can read or apply it.
        </p>
      ) : credentials.error ? (
        <p className="text-sm text-text-3">
          Failed to load the credential: {credentials.error.message}
        </p>
      ) : applied ? (
        <p className="text-sm text-text-2">
          Applied
          {stored.created_at
            ? ` ${formatRelativeTime(stored.created_at)}`
            : ""}{" "}
          for the whole deployment · stored encrypted and never returned
          {stored.last_used
            ? ` · last used ${formatRelativeTime(stored.last_used)}`
            : ""}
          {typeof stored.usage_count === "number"
            ? ` · ${formatCount(stored.usage_count)} requests`
            : ""}
          .
        </p>
      ) : (
        <>
          <p className="text-sm text-text-2">
            No shared credential is stored for this provider. One stored here
            pays every account's requests, encrypted and never returned.
          </p>
          <div>
            <PrimaryButton onClick={() => setSetting(true)}>
              Set credential
            </PrimaryButton>
          </div>
        </>
      )}

      {setting && (
        <CredentialApplyModal
          title={applied ? "Replace shared credential" : "Set shared credential"}
          description={`The shared credential for ${name}. Every account's requests can use it; it is stored encrypted and never returned.`}
          fields={fields}
          apply={async (body) => {
            if (stored) {
              applyTarget.current = stored.id;
              await updateSharedCredential(providerId, stored.id, body);
            } else {
              const created = await createSharedCredential(providerId, body);
              applyTarget.current = created.id;
            }
            await refresh();
          }}
          validate={() => {
            const credentialId = applyTarget.current;
            if (!credentialId) {
              return Promise.reject(new Error("no credential was applied"));
            }
            return validateSharedCredential(providerId, credentialId);
          }}
          onClose={() => setSetting(false)}
        />
      )}

      {confirmingRemove && stored && (
        <Modal
          title="Remove shared credential"
          onClose={() => setConfirmingRemove(false)}
          footer={
            <>
              <GhostButton onClick={() => setConfirmingRemove(false)}>
                Cancel
              </GhostButton>
              <button
                type="button"
                onClick={() => remove.mutate(stored.id)}
                disabled={remove.isPending}
                className="flex h-9 items-center rounded-sm bg-error px-4 text-sm font-medium text-white transition-opacity duration-150 ease-standard hover:opacity-90 disabled:opacity-50"
              >
                Remove
              </button>
            </>
          }
        >
          <p className="text-sm text-text-2">
            This removes the shared credential for{" "}
            <strong className="font-semibold text-text-1">{name}</strong>.
            Requests stop using it immediately; accounts fall back to the
            environment credential or their own. The stored value cannot be
            recovered.
          </p>
        </Modal>
      )}
    </section>
  );
}
