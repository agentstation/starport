import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import {
  Field,
  GhostButton,
  INPUT_CLASS,
  PrimaryButton,
} from "@/components/ui/Form";
import { Dialog, DialogBody, DialogContent, DialogError, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Select } from "@/components/ui/Select";
import {
  createAccount,
  CREDENTIAL_STRATEGY_LABELS,
  type Account,
  type CredentialStrategy,
} from "@/lib/api";
import { queries } from "@/lib/queries";

// CreateAccountModal names a new account, and optionally the account
// template it starts from. Picking a template sends only the template's id:
// an explicit field in the create request wins over the template's stamp, so
// the strategy control leaves the form while a template is chosen — the
// template answers that question, and this modal must not override it.

const STRATEGIES = Object.keys(
  CREDENTIAL_STRATEGY_LABELS,
) as CredentialStrategy[];

export function CreateAccountModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (account: Account) => void;
}) {
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("");
  const [strategy, setStrategy] =
    useState<CredentialStrategy>("operator_first");
  const [error, setError] = useState<string | null>(null);

  const templates = useQuery({
    ...queries.accountTemplates(),
  });

  const create = useMutation({
    mutationFn: () =>
      createAccount({
        id: id.trim(),
        name: name.trim() || undefined,
        ...(template
          ? { template }
          : { credential_strategy: strategy }),
      }),
    onSuccess: onCreated,
    onError: (problem) =>
      setError(problem instanceof Error ? problem.message : String(problem)),
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
          <DialogTitle>New account</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="flex flex-col gap-4">
            <Field
              label="Account ID"
              hint="The identifier keys and BYOK credentials are addressed by. It cannot change later."
            >
              <input
                value={id}
                onChange={(event) => setId(event.target.value)}
                placeholder="acme"
                autoComplete="off"
                spellCheck={false}
                className={`${INPUT_CLASS} font-mono`}
              />
            </Field>
            <Field label="Name" hint="What a person calls this account. Optional.">
              <input
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="Acme Corp"
                autoComplete="off"
                className={INPUT_CLASS}
              />
            </Field>
            {(templates.data?.length ?? 0) > 0 && (
              <Field
                label="Start from a template"
                hint="The account starts with the template's limits, credential strategy, BYOK policy, and provider access."
              >
                <Select
                  value={template}
                  onChange={(event) => setTemplate(event.target.value)}
                  aria-label="Account template"
                >
                  <option value="">No template — open defaults</option>
                  {(templates.data ?? []).map((entry) => (
                    <option key={entry.id} value={entry.id}>
                      {entry.name || entry.id}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
            {!template && (
              <Field label="Credential strategy">
                <Select
                  value={strategy}
                  onChange={(event) =>
                    setStrategy(event.target.value as CredentialStrategy)
                  }
                  aria-label="Credential strategy"
                >
                  {STRATEGIES.map((option) => (
                    <option key={option} value={option}>
                      {CREDENTIAL_STRATEGY_LABELS[option]}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
          </div>
        </DialogBody>
        <DialogError>{error}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose}>Cancel</GhostButton>
          <PrimaryButton
            onClick={() => create.mutate()}
            disabled={!id.trim() || create.isPending}
          >
            Create account
          </PrimaryButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
