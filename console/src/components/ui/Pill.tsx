import type { ReactNode } from "react";

// Pill reports a lifecycle state: a file that finished landing, a job that
// completed, a key that expired. DESIGN.md keeps the two state idioms apart.
// A dot reports liveness and a pill reports lifecycle. Every pill shares one
// recipe (tint background, solid text, full radius, 12 px medium) so a reader
// learns it once and reads it everywhere.
export type PillTone = "success" | "warning" | "error" | "info" | "neutral";

const TONES: Record<PillTone, string> = {
  success: "bg-success-tint text-success",
  warning: "bg-warning-tint text-warning",
  error: "bg-error-tint text-error",
  info: "bg-info-tint text-text-2",
  neutral: "bg-bg-raised text-text-3",
};

export function Pill({
  tone,
  title,
  children,
}: {
  tone: PillTone;
  title?: string;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className={`inline-flex h-5 items-center whitespace-nowrap rounded-full px-2 text-xs font-medium ${TONES[tone]}`}
    >
      {children}
    </span>
  );
}
