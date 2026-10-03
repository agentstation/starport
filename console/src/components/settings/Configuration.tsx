import { useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { useId, useState, type ReactNode } from "react";

import { PendingChange, shortRevision, type Drafts } from "@/components/settings/ConfigurationSave";
import { Gate } from "@/components/settings/Deployment";
import { Section } from "@/components/settings/Section";
import { GhostButton, INPUT_CLASS } from "@/components/ui/Form";
import { Pill } from "@/components/ui/Pill";
import type { CacheFillStatus, SystemInfo } from "@/lib/api";
import {
  REDACTED,
  applies,
  editable,
  expectedRevision,
  settingLabel,
  targetLabel,
  type EffectiveReport,
  type EffectiveSetting,
  type SchemaSetting,
} from "@/lib/configuration";
import { formatBytes, formatCount } from "@/lib/format";
import { queries } from "@/lib/queries";
import { cn } from "@/lib/utils";

// The task groups. A setting outside both groups, or one that the selected
// catalog source does not use, is in Advanced details.
const SOURCE_KEYS = [
  "catalog_source",
  "catalog_source_url",
  "catalog_source_api_key",
  "catalog_source_repository",
  "catalog_source_channel",
  "catalog_source_signer_workflow",
  "catalog_source_token",
  "catalog_source_refresh_mode",
  "catalog_generation_pin",
  "catalog_source_poll_interval",
  "catalog_source_startup_policy",
];

const INFERENCE_KEYS = [
  "catalog_network_mode",
  "catalog_acquisition_enabled",
  "catalog_acquisition_sources",
  "catalog_provider_bindings",
  "catalog_acquisition_interval",
];

const MANAGEMENT: Record<string, string> = {
  local: "local: this process reads its configuration file",
  shared: "shared: every process reads one revision store",
  external: "external: a controller outside Starport owns the configuration",
};

const STORE_LABEL: Record<string, string> = { kv: "Records", sql: "Relational", blobs: "File bytes" };

const LIFETIME: Record<string, string> = {
  process: "ends with the process",
  local: "kept on this machine",
  service: "kept by an external service",
};

const PLACEHOLDER: Record<string, string> = { duration: "for example 30m", boolean: "true or false" };

export function ConfigurationSection() {
  const effective = useQuery(queries.configEffective());
  const schema = useQuery(queries.configSchema());
  return (
    <Section
      title="Configuration"
      description="The catalog settings that this process serves, where each value comes from, and where a save writes. A sealed value stays sealed: the page shows only the marker that the gateway returns."
    >
      <Gate query={effective} what="the effective configuration">
        {(report: EffectiveReport | null) =>
          !report ? (
            <p className="text-md text-text-3">This gateway does not report its configuration.</p>
          ) : (
            <Gate query={schema} what="the configuration schema">
              {(settings: SchemaSetting[]) => <ConfigurationBody report={report} schema={settings} />}
            </Gate>
          )
        }
      </Gate>
    </Section>
  );
}

function ConfigurationBody({ report, schema }: { report: EffectiveReport; schema: SchemaSetting[] }) {
  const [drafts, setDrafts] = useState<Drafts>({});
  // A local save does not change the loaded file checksum, so the next save
  // names the revision that the last receipt reported.
  const [savedRevision, setSavedRevision] = useState<string | null>(null);
  const [advanced, setAdvanced] = useState(false);
  const advancedID = useId();

  const schemaByID = new Map(schema.map((setting) => [setting.id, setting]));
  const settingByKey = new Map(
    report.settings.flatMap((setting) => {
      const described = schemaByID.get(setting.id);
      return described ? [[described.key, setting] as const] : [];
    }),
  );
  const source = settingByKey.get("catalog_source")?.value ?? "";
  const expected = savedRevision ?? expectedRevision(report);
  const target = targetLabel(report, expected);
  const external = report.target.kind === "external-controller";
  const writable = !report.target.unavailable;

  function group(keys: string[]): EffectiveSetting[] {
    return keys.flatMap((key) => {
      const setting = settingByKey.get(key);
      return setting && applies(schemaByID.get(setting.id), source) ? [setting] : [];
    });
  }
  const grouped = new Set([...group(SOURCE_KEYS), ...group(INFERENCE_KEYS)].map((setting) => setting.id));
  const others = report.settings.filter((setting) => !grouped.has(setting.id));

  function row(setting: EffectiveSetting) {
    const described = schemaByID.get(setting.id);
    const key = described?.key ?? setting.id;
    return (
      <SettingRow
        key={setting.id}
        setting={setting}
        schema={described}
        source={source}
        target={target}
        canEdit={writable && editable(described)}
        draft={key in drafts ? drafts[key] : undefined}
        onDraft={(value) =>
          setDrafts((current) => {
            const next = { ...current };
            if (value === undefined) delete next[key];
            else next[key] = value;
            return next;
          })
        }
      />
    );
  }

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-md">
        <dt className="text-text-3">Managed by</dt>
        <dd className="text-text-2">{MANAGEMENT[report.management] ?? report.management}</dd>
        <dt className="text-text-3">Saves go to</dt>
        <dd className="min-w-0 break-words text-text-2">
          <span className="font-mono text-base">{target}</span>
          {report.target.unavailable && (
            <span className="block text-text-3">A save cannot write here: {report.target.unavailable}.</span>
          )}
        </dd>
        <dt className="text-text-3">Revision</dt>
        <dd className="text-text-2">
          <RevisionFact report={report} savedRevision={savedRevision} />
        </dd>
        {report.namespace && (
          <>
            <dt className="text-text-3">Namespace</dt>
            <dd className="font-mono text-base text-text-2">{report.namespace}</dd>
          </>
        )}
      </dl>

      <Group
        title="Catalog source"
        description={`Where the gateway reads its model catalog. The selected source is ${source || "not set"}. Settings that it does not use are in Advanced details.`}
      >
        {group(SOURCE_KEYS).map(row)}
      </Group>
      <Group
        title="Inference access"
        description="How the gateway reaches provider APIs for model lists and inference. Provider credentials are on the Providers screen."
      >
        {group(INFERENCE_KEYS).map(row)}
      </Group>

      <div>
        <button
          type="button"
          aria-expanded={advanced}
          aria-controls={advancedID}
          onClick={() => setAdvanced((open) => !open)}
          className="flex items-center gap-1.5 rounded-sm text-md font-medium text-text-2 hover:text-text-1"
        >
          <ChevronRight aria-hidden="true" className={cn("size-4 transition-transform duration-150", advanced && "rotate-90")} />
          Advanced details
        </button>
        {advanced && (
          <div id={advancedID} className="mt-4 flex flex-col gap-6">
            <Group title="Other settings" description="Settings outside the task groups, and settings that the selected source does not use.">
              {others.map(row)}
            </Group>
            <PathFacts report={report} />
            <StorageFacts report={report} />
            <CacheFacts />
          </div>
        )}
      </div>

      {(Object.keys(drafts).length > 0 || (!external && writable)) && (
        <PendingChange
          report={report}
          schema={schemaByID}
          drafts={drafts}
          expected={expected}
          onExpected={setSavedRevision}
          onSaved={() => setDrafts({})}
          onDiscard={() => setDrafts({})}
        />
      )}
    </div>
  );
}

function RevisionFact({ report, savedRevision }: { report: EffectiveReport; savedRevision: string | null }) {
  const revision = report.revision;
  if (report.management === "shared") {
    return (
      <>
        <span className="font-mono text-base">
          desired {revision.desired ?? 0}, applied {revision.applied ?? 0}
        </span>
        {revision.retained && (
          <span className="block text-text-3">
            The last read of the revision store failed. This process keeps serving applied revision {revision.applied ?? 0}.
          </span>
        )}
      </>
    );
  }
  const file = savedRevision ?? revision.file_checksum;
  return (
    <>
      <span className="font-mono text-base">loaded {shortRevision(revision.checksum)}</span>
      {file && file !== revision.checksum && (
        <span className="block text-text-3">
          The file is at revision {shortRevision(file)}. This process loads it again at a restart.
        </span>
      )}
    </>
  );
}

function Group({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <div>
      <h3 className="text-md font-semibold text-text-1">{title}</h3>
      <p className="mt-1 text-md text-text-3">{description}</p>
      <div className="mt-2 flex flex-col">{children}</div>
    </div>
  );
}

function SettingRow({
  setting,
  schema,
  source,
  target,
  canEdit,
  draft,
  onDraft,
}: {
  setting: EffectiveSetting;
  schema: SchemaSetting | undefined;
  source: string;
  target: string;
  canEdit: boolean;
  draft: string | null | undefined;
  onDraft: (value: string | null | undefined) => void;
}) {
  const inputID = useId();
  const label = settingLabel(schema?.key ?? setting.id);
  const sealed = setting.value === REDACTED;
  const unused = !applies(schema, source);
  return (
    <div className="flex flex-col gap-1 border-t border-border-1 py-3 first:border-t-0">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4">
        <p className="text-md text-text-1">{label}</p>
        <p className="font-mono text-base text-text-3">{schema?.environment ?? setting.name}</p>
      </div>
      <p className="flex flex-wrap items-center gap-2 text-md text-text-2">
        <span className="break-all font-mono text-base">{setting.value === "" ? "not set" : setting.value}</span>
        {sealed && <Pill tone="neutral">sealed</Pill>}
      </p>
      <p className="text-md text-text-3">
        Origin <span className="font-mono text-base">{setting.origin}</span>, authority {setting.authority}.
        {schema?.mutability === "restart" && " A change takes effect at a restart."}
        {unused && ` The ${source || "selected"} source does not use it.`}
      </p>
      {(setting.ignored ?? []).map((ignored) => (
        <p key={`${ignored.origin}-${ignored.reason}`} className="text-md text-text-3">
          Ignored <span className="font-mono text-base">{ignored.origin}</span>: {ignored.reason}
        </p>
      ))}
      {schema?.scope === "node" ? (
        <p className="text-md text-text-3">Set in the environment of each process.</p>
      ) : schema?.mutability === "migration-only" ? (
        <p className="text-md text-text-3">Changes only through starport config migrate.</p>
      ) : !canEdit ? null : draft === undefined ? (
        <div className="flex gap-2">
          <GhostButton
            aria-label={`Change ${label}`}
            onClick={() => onDraft(sealed || setting.value.includes(REDACTED) ? "" : setting.value)}
          >
            Change
          </GhostButton>
          {setting.origin !== "default" && (
            <GhostButton aria-label={`Remove ${label}`} onClick={() => onDraft(null)}>
              Remove
            </GhostButton>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {draft === null ? (
            <p className="text-md text-text-2">The pending change removes this value. The next layer supplies it.</p>
          ) : (
            <>
              <label htmlFor={inputID} className="text-md text-text-2">
                New value for {label}
              </label>
              <input
                id={inputID}
                type={schema?.sensitive ? "password" : "text"}
                autoComplete="off"
                spellCheck={false}
                value={draft}
                placeholder={PLACEHOLDER[schema?.type ?? ""]}
                onChange={(event) => onDraft(event.target.value)}
                className={cn(INPUT_CLASS, "max-w-xl font-mono")}
              />
            </>
          )}
          <p className="text-md text-text-3">
            Target: <span className="font-mono text-base">{target}</span>
          </p>
          <div>
            <GhostButton aria-label={`Discard the edit to ${label}`} onClick={() => onDraft(undefined)}>
              Discard edit
            </GhostButton>
          </div>
        </div>
      )}
    </div>
  );
}

function PathFacts({ report }: { report: EffectiveReport }) {
  const origins = Object.entries(report.paths.origins ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <Group
      title="Paths"
      description={`Where this process keeps its files. Relative paths resolve from ${report.paths.relative_path_base || "the working directory"}.`}
    >
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-md">
        <dt className="text-text-3">Configuration file</dt>
        <dd className="break-all font-mono text-base text-text-2">{report.paths.config_file}</dd>
        {origins.map(([role, path]) => (
          <div key={role} className="contents">
            <dt className="text-text-3">{role}</dt>
            <dd className="min-w-0 text-text-2">
              <span className="break-all font-mono text-base">{path.path}</span>
              <span className="ml-2 text-text-3">from {path.origin}</span>
            </dd>
          </div>
        ))}
        <dt className="text-text-3">Deployment</dt>
        <dd className="break-all font-mono text-base text-text-2">{report.paths.deployment_id}</dd>
      </dl>
    </Group>
  );
}

function StorageFacts({ report }: { report: EffectiveReport }) {
  return (
    <Group title="Storage" description="What each store keeps, and how long the data lasts.">
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-md">
        {report.storage.map((store) => (
          <div key={store.id} className="contents">
            <dt className="text-text-3">{STORE_LABEL[store.id] ?? store.id}</dt>
            <dd className="min-w-0 text-text-2">
              <span className="font-mono text-base">{store.selection}</span>, {LIFETIME[store.lifetime] ?? store.lifetime}
              {store.location && <span className="block break-all font-mono text-base text-text-3">{store.location}</span>}
            </dd>
          </div>
        ))}
      </dl>
    </Group>
  );
}

function cacheText(cache: CacheFillStatus | undefined): string {
  if (!cache?.enabled) return "off";
  const shared = cache.shared?.configured ? `shared store ${cache.shared.state ?? "unknown"}` : "this process only";
  return `on, ${shared}, ${formatCount(cache.retained_entries ?? 0)} entries, ${formatBytes(cache.retained_bytes ?? 0)}`;
}

function CacheFacts() {
  const info = useQuery(queries.systemInfo());
  return (
    <Group title="Caches" description="The optional caches and what each keeps now.">
      <Gate query={info} what="the cache state">
        {(data: SystemInfo) => (
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-md">
            <dt className="text-text-3">Responses</dt>
            <dd className="text-text-2">{cacheText(data.response_cache)}</dd>
            <dt className="text-text-3">Document extraction</dt>
            <dd className="text-text-2">{cacheText(data.extraction_cache)}</dd>
          </dl>
        )}
      </Gate>
    </Group>
  );
}
