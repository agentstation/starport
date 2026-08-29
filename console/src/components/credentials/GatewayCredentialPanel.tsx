import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useState } from "react";

import { CredentialApplyModal } from "@/components/credentials/CredentialApplyModal";
import { SourcePill } from "@/components/credentials/SourcePill";
import { GhostButton, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Modal } from "@/components/ui/Modal";
import {
  ApiError,
  deleteGatewayCredential,
  getGatewayCredential,
  putGatewayCredential,
  validateGatewayCredential,
  type CredentialField,
} from "@/lib/api";
import { formatCount, formatRelativeTime } from "@/lib/format";

// The gateway credential is the provider credential an operator applies for
// the whole deployment. It belongs to no account, so it is addressed by
// provider alone and edited here, on the provider's own screen, rather than
// anywhere near a gateway API key. On screen the word is "shared" — the
// stored half of the Shared group, beside the environment credential that is
// the same operator's money — while the keyring and the wire keep "gateway"
// (internal/providers/keyring).
//
// It is not BYOK. BYOK is a credential an account brings for itself, and it is
// managed per account. The provider credential drawer may name accounts as the
// third resolution source, but this panel never edits one and never names an
// account: the credential it applies belongs to the deployment. The section
// renders as a row of that drawer and carries no chrome of its own.

export function gatewayCredentialQueryKey(providerId: string): string[] {
  return ["gateway-credential", providerId];
}

// notApplied reads a 404 as the answer it is: this deployment has no gateway
// credential for the provider. A missing record is a state to render, not a
// failure to report.
function notApplied(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

export function GatewayCredentialPanel({
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

  const say = (text: string, error = false) => setNotice({ text, error });
  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: gatewayCredentialQueryKey(providerId),
    });

  const credential = useQuery({
    queryKey: gatewayCredentialQueryKey(providerId),
    queryFn: () => getGatewayCredential(providerId),
    retry: false,
  });

  const validate = useMutation({
    mutationFn: () => validateGatewayCredential(providerId),
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
    mutationFn: () => deleteGatewayCredential(providerId),
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
    credential.error instanceof ApiError && credential.error.needsKey;
  const missing = notApplied(credential.error);
  const applied = credential.data?.has_credentials === true;

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
              onClick={() => validate.mutate()}
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

      {credential.isPending ? (
        <p className="text-sm text-text-3">Loading credential…</p>
      ) : locked ? (
        <p className="text-sm text-text-3">
          Applied by an operator for the whole deployment. Only an operator key
          with the admin scope can read or apply it.
        </p>
      ) : credential.error && !missing ? (
        <p className="text-sm text-text-3">
          Failed to load the credential: {credential.error.message}
        </p>
      ) : applied ? (
        <p className="text-sm text-text-2">
          Applied
          {credential.data?.created_at
            ? ` ${formatRelativeTime(credential.data.created_at)}`
            : ""}{" "}
          for the whole deployment · stored encrypted and never returned
          {credential.data?.last_used
            ? ` · last used ${formatRelativeTime(credential.data.last_used)}`
            : ""}
          {typeof credential.data?.usage_count === "number"
            ? ` · ${formatCount(credential.data.usage_count)} requests`
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
            await putGatewayCredential(providerId, body);
            await refresh();
          }}
          validate={() => validateGatewayCredential(providerId)}
          onClose={() => setSetting(false)}
        />
      )}

      {confirmingRemove && (
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
                onClick={() => remove.mutate()}
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
