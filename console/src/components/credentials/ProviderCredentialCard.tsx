import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import { CredentialApplyModal } from "@/components/credentials/CredentialApplyModal";
import { GatewayCredentialPanel } from "@/components/credentials/GatewayCredentialPanel";
import {
  SourcePill,
  credentialSourcePill,
} from "@/components/credentials/SourcePill";
import { EnvironmentCredentialPanel } from "@/components/providers/ProviderDetail";
import { Field, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import {
  ApiError,
  listTenants,
  putBYOKCredential,
  validateBYOKCredential,
  type CredentialField,
  type ProviderRuntimeStatus,
} from "@/lib/api";

// ProviderCredentialCard is the one place this screen answers "what pays this
// provider". Three sources can, and the gateway tries them in a fixed default
// order (internal/providers/keyring, operator_first): the environment
// credential, then the gateway credential, then an account's own credential.
// The rows render in that order, each wearing a one-word pill, and exactly one
// pill says Active: the source requests use. An operator reads status from
// the pills and provenance from the sentences, instead of decoding what
// "read from the environment" implies about whether anything is set.
//
// The card can store an account's own credential from here — the account is
// picked in the dialog, so the address (account + provider) is always
// explicit — and links to the accounts screen that manages them. It never
// links the keys page: a gateway API key authenticates a caller and cannot
// pay a provider (AON-V26).

export function ProviderCredentialCard({
  providerId,
  name,
  credential,
  fields,
}: {
  providerId: string;
  name: string;
  credential: ProviderRuntimeStatus["operator_credential"];
  fields: CredentialField[];
}) {
  const envUsable = credential?.usable === true;
  const envPill = credentialSourcePill(credential?.state, envUsable);
  return (
    <section
      data-testid="provider-credential-card"
      className="flex flex-col rounded-md border border-border-1 bg-bg-panel"
    >
      <div className="border-b border-border-1 p-4">
        <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
          Provider credential
        </h2>
        <p className="mt-1.5 text-sm text-text-3">
          What pays {name}. A gateway API key never does — it only identifies
          the caller. Each request uses the first usable source, top to
          bottom; an account's own strategy can prefer its own credential or
          refuse the operator's.
        </p>
      </div>
      <div className="flex flex-col divide-y divide-border-1">
        <div className="flex flex-col gap-2 p-4">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-medium text-text-1">Environment</h3>
            <SourcePill
              label={envPill.label}
              tone={envPill.tone}
              title={envUsable ? "Requests use this credential" : undefined}
            />
            <span className="ml-auto text-xs text-text-4">
              read-only · set where the gateway runs
            </span>
          </div>
          <EnvironmentCredentialPanel
            providerId={providerId}
            credential={credential}
          />
        </div>
        <div className="p-4">
          <GatewayCredentialPanel
            providerId={providerId}
            name={name}
            fields={fields}
            active={!envUsable}
          />
        </div>
        <div className="p-4">
          <AccountCredentialRow
            providerId={providerId}
            name={name}
            fields={fields}
          />
        </div>
      </div>
    </section>
  );
}

// --- Accounts row: the per-account credential source (BYOK on the accounts
// screen, where the word is taught). The row can store one from here when the
// session can list accounts; managing and removing them stays on the accounts
// screen, where the stored set is visible.

function AccountCredentialRow({
  providerId,
  name,
  fields,
}: {
  providerId: string;
  name: string;
  fields: CredentialField[];
}) {
  const [adding, setAdding] = useState(false);
  const [tenantId, setTenantId] = useState("");

  const tenants = useQuery({
    queryKey: ["tenants"],
    queryFn: listTenants,
    retry: false,
  });
  const locked = tenants.error instanceof ApiError && tenants.error.needsKey;

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-medium text-text-1">Accounts</h3>
        {!locked && tenants.data && (
          <RowAction onClick={() => setAdding(true)}>
            add for an account…
          </RowAction>
        )}
      </div>
      <p className="text-sm text-text-3">
        A credential an account brings for itself. It pays that account's
        requests only, billed to the account directly, and is managed per
        account on the{" "}
        <Link
          to="/tenants"
          className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
        >
          Accounts
        </Link>{" "}
        page.
      </p>

      {adding && (
        <CredentialApplyModal
          key={tenantId}
          title={`Add account credential for ${name}`}
          description="Stored against the chosen account, encrypted and never returned. Only that account's requests use it."
          fields={fields}
          applyLabel="Store credential"
          ready={tenantId !== ""}
          apply={(body) => putBYOKCredential(tenantId, providerId, body)}
          validate={() => validateBYOKCredential(tenantId, providerId)}
          onClose={() => {
            setAdding(false);
            setTenantId("");
          }}
        >
          <Field label="Account">
            <Select
              value={tenantId}
              onChange={(event) => setTenantId(event.target.value)}
              aria-label="Account"
            >
              <option value="">Select an account…</option>
              {(tenants.data ?? []).map((tenant) => (
                <option key={tenant.id} value={tenant.id}>
                  {tenant.name || tenant.id}
                </option>
              ))}
            </Select>
          </Field>
        </CredentialApplyModal>
      )}
    </div>
  );
}
