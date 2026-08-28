import { useState, type ReactNode } from "react";

import {
  CredentialFieldInputs,
  credentialBody,
  hasSecretValue,
} from "@/components/credentials/CredentialFields";
import { GhostButton, PrimaryButton } from "@/components/ui/Form";
import { Modal } from "@/components/ui/Modal";
import type { CredentialField } from "@/lib/api";

// CredentialApplyModal is the one flow that stores a provider credential:
// apply, then immediately validate, then say what happened — in the modal,
// before it closes. An operator who pastes a key wants to know whether it
// works, not only that it saved, and the answer has to arrive while the
// pasteboard still holds the correction. Both the gateway credential and an
// account's own credential go through this flow; the caller supplies the
// address (which route, which account) and any extra pickers as children.

type Outcome = {
  valid: boolean | undefined;
  // The validation error when validation itself could not run. The apply
  // still succeeded, so this renders as uncertainty, never as failure.
  validationError?: string;
};

export function CredentialApplyModal({
  title,
  description,
  fields,
  applyLabel = "Apply credential",
  ready = true,
  children,
  apply,
  validate,
  onClose,
}: {
  title: string;
  description?: string;
  fields: CredentialField[];
  applyLabel?: string;
  // Extra gating from the caller, e.g. an account picker with no account
  // chosen yet. The secret-value gate is owned here.
  ready?: boolean;
  children?: ReactNode;
  apply: (body: {
    credentials: Record<string, string>;
    config?: Record<string, string>;
  }) => Promise<unknown>;
  validate: () => Promise<{ valid?: boolean }>;
  onClose: () => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Outcome | null>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await apply(credentialBody(fields, values));
    } catch (applyError) {
      setError(
        `Apply failed: ${applyError instanceof Error ? applyError.message : applyError}`,
      );
      setBusy(false);
      return;
    }
    try {
      const result = await validate();
      setOutcome({ valid: result?.valid !== false });
    } catch (validationError) {
      setOutcome({
        valid: undefined,
        validationError:
          validationError instanceof Error
            ? validationError.message
            : String(validationError),
      });
    }
    setBusy(false);
  };

  if (outcome) {
    return (
      <Modal
        title={title}
        onClose={onClose}
        footer={<PrimaryButton onClick={onClose}>Done</PrimaryButton>}
      >
        {outcome.valid === true ? (
          <p className="text-sm text-success">
            Applied and validated — the credential works.
          </p>
        ) : outcome.valid === false ? (
          <p className="text-sm text-warning">
            Applied, but validation failed — the provider rejected the
            credential. It is stored; replace it when you have the correct
            value.
          </p>
        ) : (
          <p className="text-sm text-text-2">
            Applied. Validation could not run
            {outcome.validationError ? `: ${outcome.validationError}` : ""}.
          </p>
        )}
      </Modal>
    );
  }

  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <GhostButton onClick={onClose} disabled={busy}>
            Cancel
          </GhostButton>
          <PrimaryButton
            onClick={() => void submit()}
            disabled={!ready || !hasSecretValue(fields, values) || busy}
          >
            {busy ? "Applying…" : applyLabel}
          </PrimaryButton>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        {description && <p className="text-sm text-text-3">{description}</p>}
        {error && <p className="text-xs text-error">{error}</p>}
        {children}
        {fields.length === 0 ? (
          // Until the caller's picker settles the address, an empty field
          // list only means "nothing chosen yet" — say nothing.
          ready && (
            <p className="text-sm text-text-3">
              This provider declares no credential contract, so there is
              nothing to apply.
            </p>
          )
        ) : (
          <CredentialFieldInputs
            fields={fields}
            values={values}
            onChange={(id, value) =>
              setValues((previous) => ({ ...previous, [id]: value }))
            }
          />
        )}
      </div>
    </Modal>
  );
}
