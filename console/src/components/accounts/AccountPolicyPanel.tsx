import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { PrimaryButton } from "@/components/ui/Form";
import {
  updateAccount,
  type Account,
  type AccountBYOKPolicy,
  type AccountProviderAccess,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import { announce, report } from "@/lib/mutations";

// AccountPolicyPanel is the operator's policy for one account: whether it may
// bring its own provider credentials, and which providers — and optionally
// which models — its requests may reach. Both rules default to everything, so
// an account with no stored policy behaves exactly as it did before the
// policy existed; the panel exists for the operator who narrows that.
//
// The gateway clears with sentinels rather than nulls: a BYOK rule of
// `{"mode":"all"}` with no providers erases the stored policy, and an access
// list of `[]` erases the stored grants. The panel sends those sentinels when
// the operator chooses the default, so choosing "everything" and never having
// chosen anything store the same thing: nothing.

type BYOKChoice = "all" | "selected" | "none";

// A granted provider maps to its granted models, where null means every
// model. The null keeps the opt-in visible in the state itself: narrowing to
// specific models is a second decision, not a side effect of granting.
type AccessDraft = {
  open: boolean;
  granted: Record<string, string[] | null>;
};

function byokDraftOf(policy: AccountBYOKPolicy | null | undefined): {
  choice: BYOKChoice;
  providers: string[];
} {
  if (policy?.mode === "selected") {
    return { choice: "selected", providers: policy.providers ?? [] };
  }
  if (policy?.mode === "none") {
    return { choice: "none", providers: [] };
  }
  return { choice: "all", providers: [] };
}

function accessDraftOf(
  access: AccountProviderAccess[] | null | undefined,
): AccessDraft {
  if (!access || access.length === 0) {
    return { open: true, granted: {} };
  }
  const granted: Record<string, string[] | null> = {};
  for (const entry of access) {
    granted[entry.provider] =
      entry.models && entry.models.length > 0 ? entry.models : null;
  }
  return { open: false, granted };
}

// ProviderChecklist is one scrollable checkbox list of providers. Both rules
// ask the same "which providers?" question, so they share the one control and
// differ only in the name their radios and boxes answer to.
function ProviderChecklist({
  idPrefix,
  providers,
  checked,
  onToggle,
}: {
  idPrefix: string;
  providers: { id: string; name?: string }[];
  checked: (providerId: string) => boolean;
  onToggle: (providerId: string) => void;
}) {
  if (providers.length === 0) {
    return (
      <p className="pl-6 text-xs text-text-4">
        No providers are in the catalog yet.
      </p>
    );
  }
  return (
    <div
      data-testid={`${idPrefix}-providers`}
      className="flex max-h-48 flex-col gap-1 overflow-y-auto pl-6"
    >
      {providers.map((provider) => (
        <label
          key={provider.id}
          className="flex items-center gap-2 text-sm text-text-2"
        >
          <input
            type="checkbox"
            checked={checked(provider.id)}
            onChange={() => onToggle(provider.id)}
            aria-label={`${idPrefix === "byok" ? "BYOK for" : "Access to"} ${provider.name || provider.id}`}
          />
          {provider.name || provider.id}
        </label>
      ))}
    </div>
  );
}

// ModelNarrowing is the model-granularity opt-in for one granted provider.
// It defaults to every model: the checkbox is the opt-in, and only opting in
// reveals the model list. Granting a provider therefore costs one click, and
// the finer decision stays a visibly separate one.
function ModelNarrowing({
  provider,
  models,
  granted,
  onChange,
}: {
  provider: { id: string; name?: string };
  models: string[];
  granted: string[] | null;
  onChange: (models: string[] | null) => void;
}) {
  const [filter, setFilter] = useState("");
  const narrowed = granted !== null;
  const chosen = granted ?? [];
  const shown = filter
    ? models.filter((model) =>
        model.toLowerCase().includes(filter.toLowerCase()),
      )
    : models;

  const toggleModel = (model: string) => {
    onChange(
      chosen.includes(model)
        ? chosen.filter((entry) => entry !== model)
        : [...chosen, model],
    );
  };

  return (
    <div className="flex flex-col gap-1 pl-6">
      <label className="flex items-center gap-2 text-xs text-text-3">
        <input
          type="checkbox"
          checked={narrowed}
          onChange={() => onChange(narrowed ? null : [])}
          aria-label={`Only specific models on ${provider.name || provider.id}`}
        />
        Only specific models
        {!narrowed && (
          <span className="text-text-4">— every model is granted</span>
        )}
      </label>
      {narrowed && (
        <>
          {models.length > 8 && (
            <input
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder="Filter models…"
              aria-label={`Filter ${provider.name || provider.id} models`}
              className="h-7 rounded-xs border border-border-1 bg-bg-base px-2 text-xs text-text-1"
            />
          )}
          <div className="flex max-h-40 flex-col gap-1 overflow-y-auto">
            {shown.map((model) => (
              <label
                key={model}
                className="flex items-center gap-2 font-mono text-xs text-text-2"
              >
                <input
                  type="checkbox"
                  checked={chosen.includes(model)}
                  onChange={() => toggleModel(model)}
                  aria-label={`Grant ${model}`}
                />
                {model}
              </label>
            ))}
            {shown.length === 0 && (
              <p className="text-xs text-text-4">
                {models.length === 0
                  ? "The catalog lists no models for this provider."
                  : "No model matches the filter."}
              </p>
            )}
          </div>
          {chosen.length === 0 && (
            <p className="text-xs text-text-4">
              Choose at least one model, or grant every model.
            </p>
          )}
        </>
      )}
    </div>
  );
}

export function AccountPolicyPanel({
  account,
  saveBody,
}: {
  // The policy shape, not necessarily an account: an account template holds
  // the same two rules, and this panel edits either.
  account: Pick<Account, "id" | "byok_policy" | "access">;
  // saveBody redirects where the rules land. The default writes the account;
  // the templates view passes its own writer.
  saveBody?: (body: {
    byok_policy?: AccountBYOKPolicy;
    access?: AccountProviderAccess[];
  }) => Promise<unknown>;
}) {
  const queryClient = useQueryClient();
  const [byok, setByok] = useState(() => byokDraftOf(account.byok_policy));
  const [access, setAccess] = useState(() => accessDraftOf(account.access));

  const catalog = useQuery({
    ...queries.providerCatalog(),
  });
  const providers = catalog.data ?? [];

  const save = useMutation({
    mutationFn: (body: {
      byok_policy?: AccountBYOKPolicy;
      access?: AccountProviderAccess[];
    }) => (saveBody ? saveBody(body) : updateAccount(account.id, body)),
    onSuccess: async () => {
      announce("Policy saved");
      await queryClient.invalidateQueries({ queryKey: queries.accounts().queryKey });
    },
    onError: (error) =>
      report(`Save failed: ${error instanceof Error ? error.message : error}`),
  });

  const toggleByokProvider = (providerId: string) => {
    setByok((draft) => ({
      choice: "selected",
      providers: draft.providers.includes(providerId)
        ? draft.providers.filter((entry) => entry !== providerId)
        : [...draft.providers, providerId],
    }));
  };

  const toggleAccessProvider = (providerId: string) => {
    setAccess((draft) => {
      const granted = { ...draft.granted };
      if (providerId in granted) {
        delete granted[providerId];
      } else {
        granted[providerId] = null;
      }
      return { open: false, granted };
    });
  };

  // The BYOK rule travels alone, and the "all" choice travels as the
  // clearing sentinel: the gateway stores no policy for the default.
  const byokBody = (): AccountBYOKPolicy => {
    if (byok.choice === "selected") {
      return { mode: "selected", providers: byok.providers };
    }
    return { mode: byok.choice };
  };

  // The access rule also travels whole, with [] as its clearing sentinel.
  // Entries follow catalog order, and a provider narrowed to models carries
  // them; one granted whole carries no models field, which is the wire's
  // word for "all models".
  const accessBody = (): AccountProviderAccess[] => {
    if (access.open) return [];
    const entries: AccountProviderAccess[] = [];
    for (const provider of providers) {
      if (!(provider.id in access.granted)) continue;
      const models = access.granted[provider.id];
      entries.push(
        models === null
          ? { provider: provider.id }
          : { provider: provider.id, models },
      );
    }
    // A granted provider the catalog no longer lists still belongs to the
    // rule: dropping it silently on save would widen nothing but would erase
    // the operator's stored intent for a provider that may return.
    for (const providerId of Object.keys(access.granted)) {
      if (providers.some((provider) => provider.id === providerId)) continue;
      const models = access.granted[providerId];
      entries.push(
        models === null
          ? { provider: providerId }
          : { provider: providerId, models },
      );
    }
    return entries;
  };

  const byokInvalid = byok.choice === "selected" && byok.providers.length === 0;
  const accessInvalid =
    !access.open &&
    (Object.keys(access.granted).length === 0 ||
      Object.values(access.granted).some(
        (models) => models !== null && models.length === 0,
      ));

  return (
    <section data-testid="account-policy-panel" className="flex flex-col gap-4">

      <fieldset className="flex flex-col gap-2">
        <legend className="text-xs font-medium text-text-2">
          May this account bring its own credentials?
        </legend>
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="radio"
            name="byok-rule"
            checked={byok.choice === "all"}
            onChange={() => setByok({ choice: "all", providers: [] })}
            className="mt-1"
          />
          <span>
            For every provider
            <span className="block text-xs text-text-4">
              The account may store its own credential on any provider.
            </span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="radio"
            name="byok-rule"
            checked={byok.choice === "selected"}
            onChange={() =>
              setByok((draft) => ({ ...draft, choice: "selected" }))
            }
            className="mt-1"
          />
          <span>
            Only for selected providers
            <span className="block text-xs text-text-4">
              Storing a credential on any other provider is refused.
            </span>
          </span>
        </label>
        {byok.choice === "selected" && (
          <ProviderChecklist
            idPrefix="byok"
            providers={providers}
            checked={(providerId) => byok.providers.includes(providerId)}
            onToggle={toggleByokProvider}
          />
        )}
        {byokInvalid && (
          <p className="pl-6 text-xs text-text-4">
            Choose at least one provider, or pick another rule.
          </p>
        )}
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="radio"
            name="byok-rule"
            checked={byok.choice === "none"}
            onChange={() => setByok({ choice: "none", providers: [] })}
            className="mt-1"
          />
          <span>
            Not at all
            <span className="block text-xs text-text-4">
              The account uses only the credentials the operator provides.
            </span>
          </span>
        </label>
        <div>
          <PrimaryButton
            onClick={() => save.mutate({ byok_policy: byokBody() })}
            disabled={byokInvalid || save.isPending}
          >
            Save BYOK rule
          </PrimaryButton>
        </div>
      </fieldset>

      <fieldset className="flex flex-col gap-2">
        <legend className="text-xs font-medium text-text-2">
          Which providers may its requests use?
        </legend>
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="radio"
            name="access-rule"
            checked={access.open}
            onChange={() => setAccess({ open: true, granted: {} })}
            className="mt-1"
          />
          <span>
            Every provider and model
            <span className="block text-xs text-text-4">
              Requests route anywhere the deployment&rsquo;s catalog reaches.
            </span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm text-text-2">
          <input
            type="radio"
            name="access-rule"
            checked={!access.open}
            onChange={() =>
              setAccess((draft) => ({ ...draft, open: false }))
            }
            className="mt-1"
          />
          <span>
            Only selected providers
            <span className="block text-xs text-text-4">
              Each granted provider serves every model unless you narrow it.
            </span>
          </span>
        </label>
        {!access.open && (
          <div className="flex flex-col gap-2">
            <ProviderChecklist
              idPrefix="access"
              providers={providers}
              checked={(providerId) => providerId in access.granted}
              onToggle={toggleAccessProvider}
            />
            {providers
              .filter((provider) => provider.id in access.granted)
              .map((provider) => (
                <div key={provider.id} className="flex flex-col gap-1 pl-6">
                  <span className="text-xs font-medium text-text-3">
                    {provider.name || provider.id}
                  </span>
                  <ModelNarrowing
                    provider={provider}
                    models={provider.models ?? []}
                    granted={access.granted[provider.id] ?? null}
                    onChange={(models) =>
                      setAccess((draft) => ({
                        open: false,
                        granted: { ...draft.granted, [provider.id]: models },
                      }))
                    }
                  />
                </div>
              ))}
            {Object.keys(access.granted).length === 0 && (
              <p className="pl-6 text-xs text-text-4">
                Choose at least one provider, or grant every provider.
              </p>
            )}
          </div>
        )}
        <div>
          <PrimaryButton
            onClick={() => save.mutate({ access: accessBody() })}
            disabled={accessInvalid || save.isPending}
          >
            Save provider access
          </PrimaryButton>
        </div>
      </fieldset>
    </section>
  );
}
