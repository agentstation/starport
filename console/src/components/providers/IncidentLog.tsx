import { Activity } from "lucide-react";
import { useState } from "react";

import { ExternalLink } from "@/components/ui/ExternalLink";
import { RelativeTime } from "@/components/ui/RelativeTime";
import type { ObservedIncidentTransition, ProviderIncidentLog } from "@/lib/api";
import { formatCount, formatRelativeTime } from "@/lib/format";

// The section shows the newest incidents and folds the rest behind one
// control, so a rough quarter does not push the offerings table off screen.
const visibleIncidents = 5;

// One severity vocabulary across both provenances: critical and major read
// as errors, minor as a warning, none as quiet. A wire that states no
// severity said only that something happened, so the chip stays neutral
// rather than guessing a level.
const SEVERITY_TONES: Record<string, string> = {
  critical: "bg-error-tint text-error",
  major: "bg-error-tint text-error",
  minor: "bg-warning-tint text-warning",
  none: "bg-bg-raised text-text-3",
};

function SeverityChip({ indicator }: { indicator: string | undefined }) {
  const label = indicator || "incident";
  return (
    <span
      className={`inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
        SEVERITY_TONES[label] ?? "bg-bg-raised text-text-3"
      }`}
    >
      {label}
    </span>
  );
}

// incidentTiming phrases one incident's lifespan from what the provider
// stated: when it started, and either how long it ran or that it is still
// open. A wire without timestamps yields nothing rather than a guess.
export function incidentTiming(
  startedAt: string | undefined,
  resolvedAt: string | undefined,
): string {
  const started = startedAt ? new Date(startedAt).getTime() : NaN;
  const resolved = resolvedAt ? new Date(resolvedAt).getTime() : NaN;
  const hasStart = Number.isFinite(started) && started > 0;
  const hasEnd = Number.isFinite(resolved) && resolved > 0;
  if (!hasStart) return hasEnd ? "resolved" : "";
  const startText = formatRelativeTime(startedAt);
  if (!hasEnd) return `started ${startText} · ongoing`;
  return `started ${startText} · lasted ${formatSpan(resolved - started)}`;
}

function formatSpan(ms: number): string {
  const minutes = Math.max(1, Math.round(ms / 60_000));
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

function IncidentEntry({
  incident,
}: {
  incident: NonNullable<ProviderIncidentLog["log"]["incidents"]>[number];
}) {
  const timing = incidentTiming(incident.started_at, incident.resolved_at);
  return (
    <li
      data-testid="incident-entry"
      className="flex flex-col gap-1 border-b border-border-1 pb-3 last:border-b-0 last:pb-0"
    >
      <div className="flex items-start gap-2">
        <SeverityChip indicator={incident.indicator} />
        {incident.url ? (
          <ExternalLink
            href={incident.url}
            className="min-w-0 text-sm font-medium text-text-1 transition-colors duration-150 ease-standard hover:text-accent-link"
          >
            {incident.title}
          </ExternalLink>
        ) : (
          <span className="min-w-0 text-sm font-medium text-text-1">
            {incident.title}
          </span>
        )}
      </div>
      {(timing || incident.status) && (
        <p className="text-xs text-text-4">
          {timing}
          {timing && incident.status ? " · " : ""}
          {incident.status}
        </p>
      )}
      {incident.update && (
        <p className="line-clamp-2 text-sm text-text-3">{incident.update}</p>
      )}
      {incident.components && incident.components.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {incident.components.map((component) => (
            <span
              key={component}
              className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3"
            >
              {component}
            </span>
          ))}
        </div>
      )}
    </li>
  );
}

// Observed transitions are this gateway's own record, kept apart from the
// provider's log so the reader always knows whose clock and whose words
// each entry carries. A transition to "none" is the recovery this gateway
// saw, so it reads as a success rather than another alarm.
function ObservedTransitions({
  observed,
}: {
  observed: ObservedIncidentTransition[];
}) {
  return (
    <div data-testid="observed-transitions" className="flex flex-col gap-2">
      <h3 className="text-xs font-medium uppercase tracking-wide text-text-4">
        Observed by this gateway
      </h3>
      <ul className="flex flex-col gap-1.5">
        {observed.map((transition, index) => (
          <li
            key={`${transition.observed_at}-${index}`}
            className="flex items-center gap-2 text-sm"
          >
            {transition.indicator === "none" ? (
              <span className="inline-flex h-5 shrink-0 items-center rounded-xs bg-success-tint px-1.5 text-xs font-medium text-success">
                cleared
              </span>
            ) : (
              <SeverityChip indicator={transition.indicator} />
            )}
            <span className="min-w-0 truncate text-text-2">
              {transition.description ||
                (transition.indicator === "none"
                  ? "The provider stopped reporting an incident."
                  : "The provider reported an incident.")}
            </span>
            <RelativeTime
              iso={transition.observed_at}
              className="ml-auto shrink-0 text-xs tabular-nums text-text-4"
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

// IncidentLog renders both incident provenances for one provider: the log
// the provider publishes about itself, and the transitions this gateway
// observed. Each availability verdict gets an honest sentence — an empty
// list, an unpublished log, and an unreachable one are three different
// facts, and only one of them is a clean record.
export function IncidentLog({
  name,
  statusPageUrl,
  report,
  failed,
}: {
  name: string;
  statusPageUrl: string | undefined;
  report: ProviderIncidentLog | undefined;
  failed: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  if (!report && !failed) return null;

  const incidents = report?.log.incidents ?? [];
  const observed = report?.observed ?? [];
  const shown = expanded ? incidents : incidents.slice(0, visibleIncidents);

  return (
    <section
      data-testid="incident-log"
      className="flex flex-col gap-3 rounded-md border border-border-1 bg-bg-panel p-4"
    >
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Incidents
      </h2>

      {failed && (
        <p className="text-sm text-text-3">
          The incident record could not be read just now.
        </p>
      )}

      {report?.log.availability === "available" &&
        (incidents.length > 0 ? (
          <>
            <ul className="flex flex-col gap-3">
              {shown.map((incident, index) => (
                <IncidentEntry
                  key={`${incident.started_at ?? incident.title}-${index}`}
                  incident={incident}
                />
              ))}
            </ul>
            {incidents.length > shown.length && (
              <button
                type="button"
                onClick={() => setExpanded(true)}
                className="self-start text-sm text-accent-link transition-colors duration-150 ease-standard hover:underline"
              >
                Show all {formatCount(incidents.length)}
              </button>
            )}
          </>
        ) : (
          <p className="text-sm text-text-3">
            No incidents reported in the last 90 days.
          </p>
        ))}

      {report?.log.availability === "unpublished" && (
        <p className="text-sm text-text-3">
          {name} does not publish a machine-readable incident log.
          {statusPageUrl && (
            <>
              {" "}
              <ExternalLink
                href={statusPageUrl}
                icon={Activity}
                className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
              >
                Check its status page
              </ExternalLink>
              .
            </>
          )}
        </p>
      )}

      {report?.log.availability === "unreachable" && (
        <p className="text-sm text-text-3">
          The provider&apos;s incident log did not answer just now.
        </p>
      )}

      {observed.length > 0 && <ObservedTransitions observed={observed} />}
    </section>
  );
}
