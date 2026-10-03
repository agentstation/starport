import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";

import { GhostButton, PrimaryButton } from "@/components/ui/Form";
import { ApiError } from "@/lib/api";
import {
  newOperationID,
  refusalOf,
  saveConfig,
  settingLabel,
  targetLabel,
  testConfigConnection,
  validateConfig,
  type EffectiveReport,
  type FieldSave,
  type Receipt,
  type Refusal,
  type SchemaSetting,
} from "@/lib/configuration";
import { queries } from "@/lib/queries";
import { cn } from "@/lib/utils";

// A pending change is the set of drafted edits, keyed by field-save key. A
// null draft removes the saved value, so the next layer supplies it.
export type Drafts = Record<string, string | null>;

type Outcome = { tone: "success" | "error" | "neutral"; text: string; retry?: { label: string; revision: string } };

// The words that replace a drafted sealed value everywhere the console
// prints the change. The typed value stays in its password input.
export const NEW_SEALED_VALUE = "new sealed value";

const CHECKSUM = /^[0-9a-f]{64}$/;

// shortRevision prints a file checksum as its first 12 characters and a
// sequence as it is.
export function shortRevision(revision: string | undefined): string {
  if (!revision) return "none";
  return CHECKSUM.test(revision) ? revision.slice(0, 12) : revision;
}

function sentence(text: string): string {
  return /[.!?]$/.test(text) ? text : `${text}.`;
}

// opening prints a gateway message that starts a sentence.
function opening(text: string): string {
  return sentence(text.charAt(0).toUpperCase() + text.slice(1));
}

function draftText(setting: SchemaSetting | undefined, value: string | null): string {
  if (value === null) return "removed";
  if (setting?.sensitive) return NEW_SEALED_VALUE;
  return value === "" ? "empty" : value;
}

function refusalText(refusal: Refusal, report: EffectiveReport): string {
  switch (refusal.reason) {
    case "stale_revision":
      return `The configuration changed after this page read it. The save named revision ${shortRevision(refusal.expected)}, and the current revision is ${shortRevision(refusal.current)}. Nothing was saved.`;
    case "operation_conflict":
      return `${opening(refusal.message)} Nothing was saved. The next save uses a new operation.`;
    case "external_management":
      return `${opening(refusal.message)} Apply the diff above in the external controller.`;
    case "schema_behind":
    case "unavailable":
      return report.management === "shared"
        ? `The shared revision store is not available. Nothing was saved. ${opening(refusal.message)}`
        : `${opening(refusal.message)} Nothing was saved.`;
    case "foreign_deployment":
      return `${opening(refusal.message)} Reload the page to read this gateway again.`;
    default:
      return `${opening(refusal.message)} Nothing was saved.`;
  }
}

function receiptText(receipt: Receipt, report: EffectiveReport): string {
  const saved =
    receipt.management === "shared"
      ? `Saved shared revision ${receipt.saved.sequence}.`
      : `Saved local file ${report.target.path ?? ""} at revision ${shortRevision(receipt.saved.revision)}.`;
  const applied = shortRevision(receipt.applied.revision);
  let state: string;
  if (receipt.activation_error) {
    state = `Not applied: ${sentence(receipt.activation_error)} This process still serves revision ${applied}.`;
  } else if (receipt.status === "applied") {
    state = "This process serves the new revision.";
  } else {
    state = `This process serves revision ${applied} until it loads the file again, for example at a restart.`;
  }
  const audit = receipt.audit_error ? ` The audit record failed: ${sentence(receipt.audit_error)}` : "";
  return `${saved} ${state}${audit}`;
}

function failureText(error: unknown, what: string): string {
  if (error instanceof ApiError && error.needsKey) return `${what} needs an admin-scoped key.`;
  return `The gateway did not complete the ${what.toLowerCase()}. ${error instanceof Error ? opening(error.message) : ""}`.trim();
}

const TONE: Record<Outcome["tone"], string> = {
  success: "text-success",
  error: "text-error",
  neutral: "text-text-2",
};

// PendingChange shows the drafted diff, states the save target before the
// save, and runs check, connection test, and save against the gateway. The
// gateway decides every result. The console only reports it.
export function PendingChange({
  report,
  schema,
  drafts,
  expected,
  onExpected,
  onSaved,
  onDiscard,
}: {
  report: EffectiveReport;
  schema: Map<string, SchemaSetting>;
  drafts: Drafts;
  expected: string;
  onExpected: (revision: string | null) => void;
  onSaved: () => void;
  onDiscard: () => void;
}) {
  const queryClient = useQueryClient();
  // One operation ID names one save. A retry after a lost answer reuses it,
  // so the gateway returns the first receipt and writes nothing twice.
  // A changed draft is a new save, so the operation also names its draft.
  const operation = useRef<{ id: string; draft: string } | null>(null);
  const [outcome, setOutcome] = useState<Outcome | null>(null);

  const keys = Object.keys(drafts).sort();
  const settingsByKey = new Map([...schema.values()].map((setting) => [setting.key, setting]));
  const valueByID = new Map(report.settings.map((setting) => [setting.id, setting.value]));
  const external = report.target.kind === "external-controller";
  const target = targetLabel(report, expected);

  function request(): FieldSave {
    return {
      operation_id: operation.current?.id ?? "",
      deployment_id: report.paths.deployment_id,
      expected_revision: expected,
      edits: { ...drafts },
    };
  }

  const check = useMutation({
    mutationFn: () => validateConfig({ ...request(), operation_id: newOperationID() }),
    onSuccess: (validation) =>
      setOutcome(
        validation.valid
          ? {
              tone: "success",
              text: `The change is valid. It makes revision ${shortRevision(validation.preview.revision)} from revision ${shortRevision(validation.current.revision)}.`,
            }
          : { tone: "error", text: validation.refusal ? refusalText(validation.refusal, report) : "The change is not valid." },
      ),
    onError: (error) => setOutcome(refused(error) ?? { tone: "error", text: failureText(error, "Check") }),
  });

  const probe = useMutation({
    mutationFn: () => testConfigConnection({ ...request(), operation_id: newOperationID() }),
    onSuccess: (result) =>
      setOutcome(
        result.reachable
          ? { tone: "success", text: `The ${result.source} catalog source answered.` }
          : { tone: "error", text: `The ${result.source} catalog source did not answer: ${sentence(result.error ?? "no reason given")}` },
      ),
    onError: (error) => setOutcome(refused(error) ?? { tone: "error", text: failureText(error, "Connection test") }),
  });

  const save = useMutation({
    mutationFn: saveConfig,
    onSuccess: (receipt) => {
      operation.current = null;
      onExpected(receipt.management === "shared" ? String(receipt.saved.sequence) : receipt.saved.revision);
      setOutcome({ tone: receipt.activation_error ? "error" : "success", text: receiptText(receipt, report) });
      void queryClient.invalidateQueries({ queryKey: queries.configEffective().queryKey });
      onSaved();
    },
    onError: (error) => {
      const refusal = refused(error);
      // A refusal is a final answer, so the next save is a new operation. A
      // lost answer keeps the operation, and a retry reads its receipt.
      if (refusal) operation.current = null;
      setOutcome(
        refusal ?? {
          tone: "error",
          text: `${failureText(error, "Save")} The save may have completed. Save again to retry the same operation.`,
        },
      );
    },
  });

  function refused(error: unknown): Outcome | null {
    const found = refusalOf(error);
    if (!found) return null;
    const { refusal } = found;
    if (refusal.reason === "stale_revision" && report.target.kind === "shared-revision") {
      // The next save names the head that the gateway reads again.
      onExpected(null);
      void queryClient.invalidateQueries({ queryKey: queries.configEffective().queryKey });
      return { tone: "error", text: `${refusalText(refusal, report)} The page read the current revision again. Review it, then save.` };
    }
    if (refusal.reason === "stale_revision" && refusal.current && report.target.kind === "local-file") {
      return { tone: "error", text: refusalText(refusal, report), retry: { label: `Save against revision ${shortRevision(refusal.current)}`, revision: refusal.current } };
    }
    return { tone: "error", text: refusalText(refusal, report) };
  }

  function runSave(revision = expected) {
    const draft = JSON.stringify(drafts);
    if (operation.current?.draft !== draft) operation.current = { id: newOperationID(), draft };
    setOutcome(null);
    save.mutate({ ...request(), operation_id: operation.current.id, expected_revision: revision });
  }

  const busy = check.isPending || probe.isPending || save.isPending;

  return (
    <div className="mt-6 flex max-w-2xl flex-col gap-3 rounded-md border border-border-1 bg-bg-panel p-4">
      <h3 className="text-md font-semibold text-text-1">Pending change</h3>
      <p className="text-md text-text-2">
        Target: <span className="font-mono text-base">{target}</span>
      </p>
      {keys.length > 0 ? (
        <ul aria-label="Pending edits" className="flex flex-col gap-1 text-md text-text-2">
          {keys.map((key) => {
            const setting = settingsByKey.get(key);
            const before = setting ? (valueByID.get(setting.id) ?? "") : "";
            return (
              <li key={key}>
                {settingLabel(key)}:{" "}
                <span className="font-mono text-base">{before === "" ? "not set" : before}</span> →{" "}
                <span className="font-mono text-base">{draftText(setting, drafts[key] ?? null)}</span>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="text-md text-text-3">No edits. Test connection checks the settings that this process serves.</p>
      )}
      {external ? (
        <p className="text-md text-text-2">
          This view is read only. Apply this diff in the external controller{report.controller ? ` (${report.controller})` : ""}.
        </p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {keys.length > 0 && (
            <PrimaryButton disabled={busy || Boolean(report.target.unavailable)} onClick={() => runSave()}>
              {save.isPending ? "Saving…" : `Save to ${target}`}
            </PrimaryButton>
          )}
          {keys.length > 0 && (
            <GhostButton disabled={busy} onClick={() => check.mutate()}>
              {check.isPending ? "Checking…" : "Check"}
            </GhostButton>
          )}
          <GhostButton disabled={busy} onClick={() => probe.mutate()}>
            {probe.isPending ? "Testing…" : "Test connection"}
          </GhostButton>
          {keys.length > 0 && (
            <GhostButton disabled={busy} onClick={onDiscard}>
              Discard
            </GhostButton>
          )}
        </div>
      )}
      <div aria-live="polite">
        {outcome && (
          <p className={cn("text-md", TONE[outcome.tone])}>
            {outcome.text}
          </p>
        )}
        {outcome?.retry && (
          <div className="mt-2">
            <GhostButton
              disabled={busy}
              onClick={() => {
                onExpected(outcome.retry?.revision ?? null);
                runSave(outcome.retry?.revision);
              }}
            >
              {outcome.retry.label}
            </GhostButton>
          </div>
        )}
      </div>
    </div>
  );
}
