import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useState } from "react";

import {
  CredentialFieldInputs,
  credentialBody,
  hasSecretValue,
} from "@/components/credentials/CredentialFields";
import { GhostButton, PrimaryButton } from "@/components/ui/Form";
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
// anywhere near a gateway API key.
//
// It is not BYOK. BYOK is a credential a tenant brings for itself, and it
// lives on the tenants screen. The two never appear on the same screen,
// because the question they answer — whose money pays for this call — has one
// answer per screen.

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
  fields,
}: {
  providerId: string;
  fields: CredentialField[];
}) {
  const queryClient = useQueryClient();
  const [values, setValues] = useState<Record<string, string>>({});
  const [editing, setEditing] = useState(false);
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

  const apply = useMutation({
    mutationFn: () =>
      putGatewayCredential(providerId, credentialBody(fields, values)),
    onSuccess: async () => {
      setValues({});
      setEditing(false);
      say("Gateway credential applied");
      await refresh();
    },
    onError: (error) =>
      say(`Apply failed: ${error instanceof Error ? error.message : error}`, true),
  });

  const validate = useMutation({
    mutationFn: () => validateGatewayCredential(providerId),
    onSuccess: (result) => {
      const valid = result?.valid !== false;
      say(
        valid
          ? "Gateway credential is valid"
          : "Gateway credential is invalid",
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
      say("Gateway credential removed");
      await refresh();
    },
    onError: (error) =>
      say(`Remove failed: ${error instanceof Error ? error.message : error}`, true),
  });

  const locked =
    credential.error instanceof ApiError && credential.error.needsKey;
  const missing = notApplied(credential.error);
  const applied = credential.data?.has_credentials === true;
  const showForm = (missing || editing) && !locked;

  return (
    <section
      data-testid="gateway-credential-panel"
      className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4"
    >
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Gateway credential
      </h2>
      <p className="text-sm text-text-3">
        Applied by the operator and used by the whole deployment. It is stored
        encrypted and never returned.
      </p>

      {notice && (
        <p className={`text-xs ${notice.error ? "text-error" : "text-success"}`}>
          {notice.text}
        </p>
      )}

      {credential.isPending ? (
        <p className="text-sm text-text-3">Loading credential…</p>
      ) : locked ? (
        <p className="text-sm text-text-3">
          Only an operator key with the admin scope can read or apply the
          deployment credential.
        </p>
      ) : credential.error && !missing ? (
        <p className="text-sm text-text-3">
          Failed to load the credential: {credential.error.message}
        </p>
      ) : applied ? (
        <div className="flex flex-wrap items-center gap-3 text-sm text-text-2">
          <span>
            Applied
            {credential.data?.created_at
              ? ` ${formatRelativeTime(credential.data.created_at)}`
              : ""}
            .
          </span>
          {credential.data?.last_used && (
            <span className="text-xs text-text-4">
              last used {formatRelativeTime(credential.data.last_used)}
              {typeof credential.data.usage_count === "number"
                ? ` · ${formatCount(credential.data.usage_count)} requests`
                : ""}
            </span>
          )}
          <div className="ml-auto flex items-center gap-1">
            <GhostButton
              onClick={() => validate.mutate()}
              disabled={validate.isPending}
            >
              validate
            </GhostButton>
            <GhostButton onClick={() => setEditing((open) => !open)}>
              {editing ? "cancel" : "replace"}
            </GhostButton>
            <button
              type="button"
              onClick={() => remove.mutate()}
              disabled={remove.isPending}
              aria-label={`Remove the ${providerId} gateway credential`}
              className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
        </div>
      ) : (
        <p className="text-sm text-text-2">
          No gateway credential is applied for this provider.
        </p>
      )}

      {showForm &&
        (fields.length === 0 ? (
          <p className="text-xs text-text-4">
            This provider declares no credential contract, so there is nothing
            to apply.
          </p>
        ) : (
          <div className="flex flex-col gap-2 border-t border-border-1 pt-3">
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
              {applied ? "Replace credential" : "Apply credential"}
            </PrimaryButton>
          </div>
        ))}
    </section>
  );
}
