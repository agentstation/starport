import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { useState } from "react";

import { AccountPolicyPanel } from "@/components/accounts/AccountPolicyPanel";
import {
  Field,
  GhostButton,
  INPUT_CLASS,
  PrimaryButton,
} from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import {
  createAccountTemplate,
  CREDENTIAL_STRATEGY_LABELS,
  deleteAccountTemplate,
  updateAccountTemplate,
  type AccountTemplate,
  type CredentialStrategy,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import { announce, report } from "@/lib/mutations";

// AccountTemplatesPanel manages the account templates this gateway holds. A
// template names creation defaults once — limits, credential strategy, BYOK
// policy, provider access — and stamps copies onto each account created from
// it. Editing here changes only what future accounts start with: an account
// already created keeps what it was stamped with.

const STRATEGIES = Object.keys(
  CREDENTIAL_STRATEGY_LABELS,
) as CredentialStrategy[];

// TemplateEditor edits one template: its two named fields, and the same
// policy rules an account has, saved to the template instead of any account.
function TemplateEditor({ template }: { template: AccountTemplate }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(template.name ?? "");
  const [strategy, setStrategy] = useState<CredentialStrategy>(
    template.credential_strategy ?? "operator_first",
  );
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () =>
      updateAccountTemplate(template.id, {
        name: name.trim() || undefined,
        credential_strategy: strategy,
      }),
    onSuccess: async () => {
      setError(null);
      await queryClient.invalidateQueries({ queryKey: queries.accountTemplates().queryKey });
    },
    onError: (problem) =>
      setError(problem instanceof Error ? problem.message : String(problem)),
  });

  return (
    <div className="flex flex-col gap-4 border-t border-border-1 pt-4">
      <Field label="Name">
        <input
          value={name}
          onChange={(event) => setName(event.target.value)}
          aria-label="Template name"
          autoComplete="off"
          className={INPUT_CLASS}
        />
      </Field>
      <Field
        label="Credential strategy"
        hint="What an account created from this template starts with."
      >
        <Select
          value={strategy}
          onChange={(event) =>
            setStrategy(event.target.value as CredentialStrategy)
          }
          aria-label="Template credential strategy"
        >
          {STRATEGIES.map((option) => (
            <option key={option} value={option}>
              {CREDENTIAL_STRATEGY_LABELS[option]}
            </option>
          ))}
        </Select>
      </Field>
      <div>
        <PrimaryButton
          onClick={() => save.mutate()}
          disabled={save.isPending}
        >
          Save template
        </PrimaryButton>
      </div>
      {error && <p className="text-xs text-error">Save failed: {error}</p>}

      {/* The same policy editor an account has, landing on the template. */}
      <AccountPolicyPanel
        key={template.id}
        account={template}
        saveBody={async (body) => {
          const saved = await updateAccountTemplate(template.id, body);
          await queryClient.invalidateQueries({ queryKey: queries.accountTemplates().queryKey });
          return saved;
        }}
      />
    </div>
  );
}

// CreateTemplateForm names a new template. Policy rules come after creation,
// through the same editor an existing template offers.
function CreateTemplateForm({ onCreated }: { onCreated: () => void }) {
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [strategy, setStrategy] =
    useState<CredentialStrategy>("operator_first");
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      createAccountTemplate({
        id: id.trim(),
        name: name.trim() || undefined,
        credential_strategy: strategy,
      }),
    onSuccess: onCreated,
    onError: (problem) =>
      setError(problem instanceof Error ? problem.message : String(problem)),
  });

  return (
    <div className="flex flex-col gap-4 border-t border-border-1 pt-4">
      <Field
        label="Template ID"
        hint="How the create-account request names this template. It cannot change later."
      >
        <input
          value={id}
          onChange={(event) => setId(event.target.value)}
          aria-label="Template ID"
          placeholder="team-default"
          autoComplete="off"
          spellCheck={false}
          className={`${INPUT_CLASS} font-mono`}
        />
      </Field>
      <Field label="Name" hint="What a person calls this template. Optional.">
        <input
          value={name}
          onChange={(event) => setName(event.target.value)}
          aria-label="Template name"
          autoComplete="off"
          className={INPUT_CLASS}
        />
      </Field>
      <Field label="Credential strategy">
        <Select
          value={strategy}
          onChange={(event) =>
            setStrategy(event.target.value as CredentialStrategy)
          }
          aria-label="Template credential strategy"
        >
          {STRATEGIES.map((option) => (
            <option key={option} value={option}>
              {CREDENTIAL_STRATEGY_LABELS[option]}
            </option>
          ))}
        </Select>
      </Field>
      <div>
        <PrimaryButton
          onClick={() => create.mutate()}
          disabled={!id.trim() || create.isPending}
        >
          Create template
        </PrimaryButton>
      </div>
      {error && <p className="text-xs text-error">Create failed: {error}</p>}
    </div>
  );
}

export function AccountTemplatesPanel({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const templates = useQuery({
    ...queries.accountTemplates(),
  });

  const remove = useMutation({
    mutationFn: (templateId: string) => deleteAccountTemplate(templateId),
    onSuccess: async (_result, templateId) => {
      announce(`Template ${templateId} deleted`);
      setOpen((current) => (current === templateId ? null : current));
      await queryClient.invalidateQueries({ queryKey: queries.accountTemplates().queryKey });
    },
    onError: (error) =>
      report(`Delete failed: ${error instanceof Error ? error.message : error}`),
  });

  const rows = templates.data ?? [];

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent>
        <SheetHeader>
          <SheetTitle>Account templates</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <div className="flex flex-col gap-4">
            <p className="text-sm text-text-3">
              An account template names creation defaults once. Creating an
              account from it stamps copies, so editing a template never rewrites
              an account it already created.
            </p>


            {templates.error ? (
              <p className="text-sm text-text-3">
                Failed to load templates:{" "}
                {templates.error instanceof Error
                  ? templates.error.message
                  : String(templates.error)}
              </p>
            ) : templates.isPending ? (
              <p className="text-sm text-text-3">Loading templates…</p>
            ) : rows.length === 0 ? (
              <p className="text-sm text-text-3">No templates yet.</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {rows.map((template) => (
                  <li
                    key={template.id}
                    className="rounded-sm border border-border-1 bg-bg-panel px-3 py-2"
                  >
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => {
                          setCreating(false);
                          setOpen(open === template.id ? null : template.id);
                        }}
                        className="flex-1 text-left text-sm text-text-1 transition-colors duration-150 ease-standard hover:text-accent-link"
                      >
                        {template.name || template.id}
                        <span className="ml-2 font-mono text-xs text-text-4">
                          {template.id}
                        </span>
                      </button>
                      <span className="text-xs text-text-3">
                        {
                          CREDENTIAL_STRATEGY_LABELS[
                            template.credential_strategy ?? "operator_first"
                          ]
                        }
                      </span>
                      <button
                        type="button"
                        onClick={() => remove.mutate(template.id)}
                        disabled={remove.isPending}
                        aria-label={`Delete the ${template.id} template`}
                        className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error disabled:opacity-50"
                      >
                        <Trash2 className="size-3.5" />
                      </button>
                    </div>
                    {open === template.id && (
                      <div className="mt-3">
                        <TemplateEditor key={template.id} template={template} />
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}

            {creating ? (
              <CreateTemplateForm
                onCreated={async () => {
                  setCreating(false);
                  announce("Template created");
                  await queryClient.invalidateQueries({ queryKey: queries.accountTemplates().queryKey });
                }}
              />
            ) : (
              <div>
                <GhostButton
                  onClick={() => {
                    setOpen(null);
                    setCreating(true);
                  }}
                >
                  New template
                </GhostButton>
              </div>
            )}
          </div>
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}
