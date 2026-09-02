import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useRef, useState } from "react";

import { CredentialApplyModal } from "@/components/credentials/CredentialApplyModal";
import {
  SharedCredentialPanel,
} from "@/components/credentials/SharedCredentialPanel";
import { SourcePill } from "@/components/credentials/SourcePill";
import {
  EnvironmentCredentialPanel,
  operatorEnvNames,
} from "@/components/providers/ProviderDetail";
import { Field, PrimaryButton, RowAction } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import {
  ApiError,
  createSharedCredential,
  putBYOKCredential,
  validateBYOKCredential,
  validateSharedCredential,
  type CredentialField,
  type ProviderRuntimeStatus,
} from "@/lib/api";
import { queries } from "@/lib/queries";

// ProviderCredentialCard answers "what pays this provider" in one line: the
// effective payer, named. Everything else — the full source list, provenance,
// set/replace/remove — lives behind Manage, in a drawer, so a configured
// provider spends one row of the page instead of a column.
//
// The sources split into two owners. Shared credentials are the operator's
// money — one read from the process environment, and the ones stored through
// the console — and the deployment's accounts' requests can use them. An
// account's own credential pays that account alone and is managed on the
// accounts screen.
// A request uses the first usable source in the keyring's default order
// (internal/providers/keyring, operator_first): environment, then stored,
// then the account's own.
//
// Who is reading is inferred from what the gateway answers, not asked: the
// stored-credential read needs the admin scope, which a localhost console
// session holds, so a locked read means an account-side reader and the card
// says so instead of offering controls that would 403. Nothing here links the
// keys page: a gateway API key authenticates a caller and cannot pay a
// provider (AON-V26).

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
  const queryClient = useQueryClient();
  const [managing, setManaging] = useState(false);
  const [settingShared, setSettingShared] = useState(false);
  // The id the empty-state modal's validate call addresses; the create
  // response supplies it, after apply and before validate.
  const createdShared = useRef<string | null>(null);

  const stored = useQuery({
    ...queries.sharedCredentials(providerId),
  });

  const envUsable = credential?.usable === true;
  const locked = stored.error instanceof ApiError && stored.error.needsKey;
  const storedApplied = (stored.data?.length ?? 0) > 0;
  // Resolution walks the list in order, so the first stored credential is
  // the one a request reaches first; the payer line names it.
  const servingLabel = stored.data?.[0]?.label;
  const [envName, prefixedEnvName] = operatorEnvNames(providerId);

  return (
    <section
      data-testid="provider-credential-card"
      className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-medium text-text-2">
          Credential
        </h2>
        <RowAction onClick={() => setManaging(true)}>manage…</RowAction>
      </div>

      {envUsable ? (
        <p className="flex flex-wrap items-center gap-2 text-sm text-text-2">
          <SourcePill
            label="Active"
            tone="success"
            title="Requests use this credential"
          />
          <span>
            Paid by the shared environment credential (
            <code className="font-mono text-xs">{envName}</code>), set where
            the gateway runs.
          </span>
        </p>
      ) : storedApplied ? (
        <p className="flex flex-wrap items-center gap-2 text-sm text-text-2">
          <SourcePill
            label="Active"
            tone="success"
            title="Requests use this credential"
          />
          <span>
            Paid by the shared credential stored on this gateway
            {servingLabel ? (
              <>
                {" "}
                (<strong className="font-medium">{servingLabel}</strong>)
              </>
            ) : null}
            , applied by an operator.
          </span>
        </p>
      ) : stored.isPending ? (
        <p className="text-sm text-text-3">Reading credential state…</p>
      ) : locked ? (
        <p className="text-sm text-text-3">
          Shared credentials are applied by an operator. Your account can bring
          its own credential for {name} on the{" "}
          <AccountsLink label="Accounts" /> page.
        </p>
      ) : (
        // The empty state is the setup lesson: what pays, and the three ways
        // to make one exist, with the console-stored shared credential as the
        // primary action because it is the one this screen can finish.
        <div className="flex flex-col gap-2.5">
          <p className="text-sm text-text-2">
            Nothing pays {name} yet, so requests to it fail. A gateway API key
            only identifies the caller — a provider needs one of these:
          </p>
          <ul className="flex list-disc flex-col gap-1 pl-5 text-sm text-text-3">
            <li>
              A shared credential stored here — an operator sets it once and
              every account&rsquo;s requests can use it.
            </li>
            <li>
              A shared environment credential:{" "}
              <code className="font-mono text-xs">{envName}</code> or{" "}
              <code className="font-mono text-xs">{prefixedEnvName}</code>, set
              where the gateway runs.
            </li>
            <li>
              An account&rsquo;s own credential, added on the{" "}
              <AccountsLink label="Accounts" /> page — it pays that
              account&rsquo;s requests only.
            </li>
          </ul>
          <div>
            <PrimaryButton onClick={() => setSettingShared(true)}>
              Set shared credential
            </PrimaryButton>
          </div>
        </div>
      )}

      {settingShared && (
        <CredentialApplyModal
          title="Set shared credential"
          description={`The shared credential for ${name}. Every account's requests can use it; it is stored encrypted and never returned.`}
          fields={fields}
          apply={async (body) => {
            const created = await createSharedCredential(providerId, body);
            createdShared.current = created.id;
            await Promise.all([
              queryClient.invalidateQueries({
                queryKey: queries.sharedCredentials(providerId).queryKey,
              }),
              queryClient.invalidateQueries({
                queryKey: queries.providerStatus().queryKey,
              }),
            ]);
          }}
          validate={() => {
            const credentialId = createdShared.current;
            if (!credentialId) {
              return Promise.reject(new Error("no credential was applied"));
            }
            return validateSharedCredential(providerId, credentialId);
          }}
          onClose={() => setSettingShared(false)}
        />
      )}

      {managing && (
        <Sheet
          open
          onOpenChange={(open) => {
            if (!open) setManaging(false);
          }}
        >
          <SheetContent>
            <SheetHeader>
              <SheetTitle>{`${name} credentials`}</SheetTitle>
            </SheetHeader>
            <SheetBody>
              <div className="flex flex-col gap-5">
                <div className="flex flex-col gap-3">
                  <div>
                    <h3 className="text-xs font-medium text-text-3">
                      Shared
                    </h3>
                    <p className="mt-1 text-xs text-text-4">
                      The operator&rsquo;s credentials. Every account&rsquo;s
                      requests can use them.
                    </p>
                  </div>
                  <div className="flex flex-col gap-2 rounded-sm border border-border-1 bg-bg-panel p-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <h4 className="text-sm font-medium text-text-1">
                        Environment
                      </h4>
                      {envUsable && (
                        <SourcePill
                          label="Active"
                          tone="success"
                          title="Requests use this credential"
                        />
                      )}
                      {!envUsable && <SourcePill label="Not set" tone="neutral" />}
                      <span className="ml-auto text-xs text-text-4">
                        read-only · set where the gateway runs
                      </span>
                    </div>
                    <EnvironmentCredentialPanel
                      providerId={providerId}
                      credential={credential}
                    />
                  </div>
                  <div className="rounded-sm border border-border-1 bg-bg-panel p-3">
                    <SharedCredentialPanel
                      providerId={providerId}
                      name={name}
                      fields={fields}
                      active={!envUsable}
                    />
                  </div>
                </div>
                <div className="flex flex-col gap-3">
                  <div>
                    <h3 className="text-xs font-medium text-text-3">
                      Accounts
                    </h3>
                    <p className="mt-1 text-xs text-text-4">
                      A credential an account brings for itself. It pays that
                      account&rsquo;s requests only, billed to the account
                      directly.
                    </p>
                  </div>
                  <AccountCredentialRow
                    providerId={providerId}
                    name={name}
                    fields={fields}
                  />
                </div>
              </div>
            </SheetBody>
            <SheetFooter>
              <p className="text-xs text-text-4">
                            Each request uses the first usable source: shared environment,
                            then shared stored, then the account&rsquo;s own. An
                            account&rsquo;s strategy can prefer its own credential or refuse
                            the operator&rsquo;s.
                          </p>
            </SheetFooter>
          </SheetContent>
        </Sheet>
      )}
    </section>
  );
}

function AccountsLink({ label }: { label: string }) {
  return (
    <Link
      to="/accounts"
      className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
    >
      {label}
    </Link>
  );
}

// --- Accounts group: the per-account credential source (BYOK on the accounts
// screen, where the word is taught). The drawer can store one from here when
// the session can list accounts; managing and removing them stays on the
// accounts screen, where the stored set is visible.

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
  const [accountId, setAccountId] = useState("");

  const accounts = useQuery({
    ...queries.accounts(),
  });
  const locked = accounts.error instanceof ApiError && accounts.error.needsKey;

  return (
    <div className="flex min-w-0 flex-col gap-2 rounded-sm border border-border-1 bg-bg-panel p-3">
      <p className="text-sm text-text-3">
        Managed per account on the <AccountsLink label="Accounts" /> page.
      </p>
      {!locked && accounts.data && (
        <div>
          <RowAction onClick={() => setAdding(true)}>
            add for an account…
          </RowAction>
        </div>
      )}

      {adding && (
        <CredentialApplyModal
          key={accountId}
          title={`Add account credential for ${name}`}
          description="Stored against the chosen account, encrypted and never returned. Only that account's requests use it."
          fields={fields}
          applyLabel="Store credential"
          ready={accountId !== ""}
          apply={(body) => putBYOKCredential(accountId, providerId, body)}
          validate={() => validateBYOKCredential(accountId, providerId)}
          onClose={() => {
            setAdding(false);
            setAccountId("");
          }}
        >
          <Field label="Account">
            <Select
              value={accountId}
              onChange={(event) => setAccountId(event.target.value)}
              aria-label="Account"
            >
              <option value="">Select an account…</option>
              {(accounts.data ?? []).map((account) => (
                <option key={account.id} value={account.id}>
                  {account.name || account.id}
                </option>
              ))}
            </Select>
          </Field>
        </CredentialApplyModal>
      )}
    </div>
  );
}
