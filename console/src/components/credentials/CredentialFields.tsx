import type { CredentialField } from "@/lib/api";
import { INPUT_CLASS } from "@/components/ui/Form";

// The catalog declares what a provider's credential is made of, so neither
// credential plane assumes an "api key". This module owns that contract for
// both: the operator's gateway credential on a provider screen, and a tenant's
// BYOK credential on the tenants screen. They differ in who owns the value and
// which route stores it. The fields a provider declares are the same either
// way, and stating them twice would let the two drift.

export type CredentialDraft = {
  credentials: Record<string, string>;
  config: Record<string, string>;
};

// splitCredentialValues sorts a filled form into what is encrypted and what is
// not. A field's kind is the catalog's statement about which it is, and an
// empty value is left out entirely rather than sent as a blank — a PUT is an
// upsert, and a blank secret would rotate a working credential to nothing.
export function splitCredentialValues(
  fields: CredentialField[],
  values: Record<string, string>,
): CredentialDraft {
  const draft: CredentialDraft = { credentials: {}, config: {} };
  for (const field of fields) {
    const value = values[field.id]?.trim();
    if (!value) continue;
    if (field.kind === "secret") draft.credentials[field.id] = value;
    else draft.config[field.id] = value;
  }
  return draft;
}

// hasSecretValue reports whether the form carries the part that has to be
// present. A credential with only its configuration filled in is not a
// credential, so the submit control stays disabled until a secret is typed.
export function hasSecretValue(
  fields: CredentialField[],
  values: Record<string, string>,
): boolean {
  return fields.some(
    (field) => field.kind === "secret" && Boolean(values[field.id]?.trim()),
  );
}

export function credentialBody(
  fields: CredentialField[],
  values: Record<string, string>,
): { credentials: Record<string, string>; config?: Record<string, string> } {
  const draft = splitCredentialValues(fields, values);
  return {
    credentials: draft.credentials,
    ...(Object.keys(draft.config).length > 0 ? { config: draft.config } : {}),
  };
}

export function CredentialFieldInputs({
  fields,
  values,
  onChange,
}: {
  fields: CredentialField[];
  values: Record<string, string>;
  onChange: (id: string, value: string) => void;
}) {
  return (
    <>
      {fields.map((field) => (
        <input
          key={field.id}
          type={field.kind === "secret" ? "password" : "text"}
          value={values[field.id] ?? ""}
          onChange={(event) => onChange(field.id, event.target.value)}
          placeholder={
            field.default ? `${field.id} (${field.default})` : field.id
          }
          autoComplete="off"
          spellCheck={false}
          aria-label={field.id}
          className={`${INPUT_CLASS} font-mono`}
        />
      ))}
    </>
  );
}
