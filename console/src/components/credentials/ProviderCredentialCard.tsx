import { Link } from "@tanstack/react-router";

import { GatewayCredentialPanel } from "./GatewayCredentialPanel";
import type { CredentialField, ProviderRuntimeStatus } from "@/lib/api";

import { EnvironmentCredentialPanel } from "@/components/providers/ProviderDetail";

// ProviderCredentialCard is the one place this screen answers "what pays this
// provider". Three sources can, and the gateway tries them in a fixed default
// order (internal/providers/keyring, operator_first): the environment
// credential, then the gateway credential, then an account's own BYOK
// credential. Rendering the three as a numbered chain, in that order, is the
// design: an operator who once read three sibling panels as three unrelated
// keys now reads one fallback sequence.
//
// The card explains BYOK and links to the accounts screen that manages it,
// but it never edits one — a per-account credential is edited where the
// account lives. It never links the keys page: a gateway API key
// authenticates a caller and cannot pay a provider (AON-V26).

function SourceMarker({ order }: { order: number }) {
  return (
    <span
      aria-hidden="true"
      className="mt-px flex size-5 shrink-0 items-center justify-center rounded-full bg-bg-raised font-mono text-[11px] leading-none text-text-3"
    >
      {order}
    </span>
  );
}

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
          the caller. Each request tries these sources in order until one is
          usable:
        </p>
      </div>
      <ol className="flex flex-col divide-y divide-border-1">
        <li className="flex gap-3 p-4">
          <SourceMarker order={1} />
          <EnvironmentCredentialPanel
            providerId={providerId}
            credential={credential}
          />
        </li>
        <li className="flex gap-3 p-4">
          <SourceMarker order={2} />
          <GatewayCredentialPanel providerId={providerId} fields={fields} />
        </li>
        <li className="flex gap-3 p-4">
          <SourceMarker order={3} />
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
              BYOK credential
            </h2>
            <p className="text-sm text-text-3">
              A credential an account brings for itself. It pays that
              account's requests only, billed to the account directly, and is
              managed per account on the{" "}
              <Link
                to="/tenants"
                className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
              >
                Accounts
              </Link>{" "}
              page. An account's credential strategy can also prefer its own
              credential first, or refuse the operator's entirely.
            </p>
          </div>
        </li>
      </ol>
    </section>
  );
}
