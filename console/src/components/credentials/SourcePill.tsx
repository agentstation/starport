// SourcePill is the one-word status a credential source wears (DESIGN.md:
// pills report lifecycle). The vocabulary is deliberately small: Active is
// the source requests use, Applied is stored but shadowed by an earlier
// source, Not set is absent, and a failure state names itself. Anything
// longer than a word belongs in the row's own sentence, not the pill.

const TONES = {
  success: "bg-success-tint text-success",
  info: "bg-info-tint text-text-2",
  neutral: "bg-bg-raised text-text-3",
  warning: "bg-warning-tint text-warning",
  error: "bg-error-tint text-error",
} as const;

export type SourceTone = keyof typeof TONES;

export function SourcePill({
  label,
  tone,
  title,
}: {
  label: string;
  tone: SourceTone;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={`inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${TONES[tone]}`}
    >
      {label}
    </span>
  );
}

// credentialSourcePill maps a runtime credential state onto the pill
// vocabulary. `active` marks the source requests currently use.
export function credentialSourcePill(
  state: string | undefined,
  usable: boolean,
): { label: string; tone: SourceTone } {
  if (usable) return { label: "Active", tone: "success" };
  switch (state ?? "not_configured") {
    case "not_configured":
      return { label: "Not set", tone: "neutral" };
    case "refreshing":
      return { label: "Refreshing", tone: "info" };
    case "unavailable":
      return { label: "Unavailable", tone: "warning" };
    case "denied":
      return { label: "Denied", tone: "error" };
    case "invalid":
      return { label: "Invalid", tone: "error" };
    default:
      return { label: (state ?? "unknown").replaceAll("_", " "), tone: "neutral" };
  }
}
