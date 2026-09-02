import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";

import * as api from "./api";
import type { ActivityFilters, ActivityPage } from "./api";

// queries owns every query key and fetcher the console reads. A route or a
// component spreads one factory into useQuery and adds only what the call
// site knows: whether the gateway is reachable, a refetch cadence tied to the
// screen, or a select that narrows the record to the slice it renders.
//
// Every factory starts its key with a prefix no other factory uses, so an
// invalidation by prefix reaches exactly one resource. queries.test.ts holds
// that rule.

const MINUTE = 60_000;

// Catalog reads change once a day at most. Runtime reads change within a
// minute. The client default in main.tsx covers everything else.
const CATALOG_STALE = 5 * MINUTE;
const RUNTIME_STALE = MINUTE;

// ActivityScope is what the usage page learned about its credential from one
// probe: the admin listing answered, the admin listing refused it, or no
// activity store is configured. A refused own listing surfaces as the error
// of the activity query itself, so the probe needs one request, not two.
export type ActivityScope = "admin" | "own" | "unconfigured";

export type ActivityQuery = {
  scope: ActivityScope;
  filters: ActivityFilters;
  sinceISO?: string;
};

// widestActivity reads the widest activity listing this credential reaches.
// An admin credential sees the deployment. Every other credential sees the
// account it belongs to.
async function widestActivity(filters: ActivityFilters): Promise<ActivityPage> {
  try {
    return await api.listAdminActivity(filters);
  } catch (error) {
    if (!(error instanceof api.ApiError) || !error.needsKey) throw error;
  }
  return api.listActivity(filters);
}

export const queries = {
  health: () =>
    queryOptions({
      queryKey: ["health"],
      queryFn: () => api.healthReady(),
      refetchInterval: 30_000,
    }),
  systemInfo: () =>
    queryOptions({ queryKey: ["system-info"], queryFn: () => api.systemInfo() }),
  systemMetrics: () =>
    queryOptions({ queryKey: ["system-metrics"], queryFn: () => api.systemMetrics() }),
  authMode: () =>
    queryOptions({ queryKey: ["auth-mode"], queryFn: () => api.readAuthMode() }),

  models: () =>
    queryOptions({
      queryKey: ["models"],
      queryFn: () => api.listModels(),
      staleTime: CATALOG_STALE,
    }),
  authors: () =>
    queryOptions({
      queryKey: ["authors"],
      queryFn: () => api.listAuthors(),
      staleTime: CATALOG_STALE,
    }),
  author: (authorId: string) =>
    queryOptions({
      queryKey: ["author", authorId],
      queryFn: () => api.getAuthor(authorId),
      staleTime: CATALOG_STALE,
    }),
  providerCatalog: () =>
    queryOptions({
      queryKey: ["provider-catalog"],
      queryFn: () => api.listProviderCatalog(),
      staleTime: CATALOG_STALE,
    }),
  catalogMetadata: () =>
    queryOptions({
      queryKey: ["catalog-metadata"],
      queryFn: () => api.catalogMetadata(),
      staleTime: CATALOG_STALE,
    }),
  catalogChanges: () =>
    queryOptions({
      queryKey: ["catalog-changes"],
      queryFn: () => api.catalogChanges(),
      staleTime: CATALOG_STALE,
    }),

  providerStatus: () =>
    queryOptions({
      queryKey: ["provider-status"],
      queryFn: () => api.providerStatus(),
      staleTime: RUNTIME_STALE,
    }),
  providerIncidents: (providerId: string) =>
    queryOptions({
      queryKey: ["provider-incidents", providerId],
      queryFn: () => api.providerIncidentLog(providerId),
    }),
  // The window is part of the key so a refetch keeps its bounds.
  providerActivity: (providerId: string, sinceISO: string) =>
    queryOptions({
      queryKey: ["provider-activity", providerId, sinceISO],
      queryFn: () => widestActivity({ provider: providerId, since: sinceISO, limit: 200 }),
    }),

  keys: () => queryOptions({ queryKey: ["keys"], queryFn: () => api.listKeys() }),
  keyDetail: (keyId: string) =>
    queryOptions({
      queryKey: ["key-detail", keyId],
      queryFn: () => api.getKeyDetail(keyId),
    }),
  sharedCredentials: (providerId: string) =>
    queryOptions({
      queryKey: ["shared-credentials", providerId],
      queryFn: () => api.listSharedCredentials(providerId),
    }),
  byokCredentials: (accountId: string) =>
    queryOptions({
      queryKey: ["byok", accountId],
      queryFn: () => api.listBYOKCredentials(accountId),
    }),

  accounts: () =>
    queryOptions({ queryKey: ["accounts"], queryFn: () => api.listAccounts() }),
  accountTemplates: () =>
    queryOptions({
      queryKey: ["account-templates"],
      queryFn: () => api.listAccountTemplates(),
    }),
  members: () =>
    queryOptions({ queryKey: ["members"], queryFn: () => api.listMembers() }),
  memberGrants: (userId: string) =>
    queryOptions({
      queryKey: ["member-grants", userId],
      queryFn: () => api.listMemberGrants(userId),
    }),
  reachableAccounts: (userId: string) =>
    queryOptions({
      queryKey: ["reachable-accounts", userId],
      queryFn: () => api.listReachableAccounts(userId),
    }),
  teams: () => queryOptions({ queryKey: ["teams"], queryFn: () => api.listTeams() }),
  teamMembers: (teamId: string) =>
    queryOptions({
      queryKey: ["team-members", teamId],
      queryFn: () => api.listTeamMembers(teamId),
    }),
  teamGrants: (teamId: string) =>
    queryOptions({
      queryKey: ["team-grants", teamId],
      queryFn: () => api.listTeamGrants(teamId),
    }),

  files: () => queryOptions({ queryKey: ["files"], queryFn: () => api.listFiles() }),
  videoJobs: () =>
    queryOptions({ queryKey: ["video-jobs"], queryFn: () => api.listJobs() }),
  documentActivity: () =>
    queryOptions({
      queryKey: ["document-activity"],
      queryFn: async () => (await widestActivity({ limit: 200 })).data ?? [],
    }),

  presets: () =>
    queryOptions({
      queryKey: ["presets"],
      queryFn: () => api.listPresets(),
      staleTime: RUNTIME_STALE,
    }),
  presetHistory: (name: string) =>
    queryOptions({
      queryKey: ["preset-history", name],
      queryFn: () => api.listPresetHistory(name),
    }),

  // One probe decides the usage scope for the whole session. A credential
  // change invalidates the client, which is the only event that moves it.
  activityScope: () =>
    queryOptions({
      queryKey: ["activity-scope"],
      queryFn: async (): Promise<ActivityScope> => {
        try {
          await api.listAdminActivity({ limit: 1 });
          return "admin";
        } catch (error) {
          if (error instanceof api.ApiError && error.status === 503) return "unconfigured";
          if (error instanceof api.ApiError && error.needsKey) return "own";
          throw error;
        }
      },
      staleTime: 30 * MINUTE,
    }),
  activity: ({ scope, filters, sinceISO }: ActivityQuery) =>
    infiniteQueryOptions({
      queryKey: ["activity", { scope, ...filters, since: sinceISO }],
      initialPageParam: "",
      queryFn: ({ pageParam }) => {
        const page = { ...filters, since: sinceISO, cursor: pageParam || undefined };
        return scope === "admin"
          ? api.listAdminActivity(page)
          : api.listActivity({ ...page, key_id: undefined });
      },
      getNextPageParam: (last) => last.next_cursor || undefined,
    }),
  adminActivity24h: () =>
    queryOptions({
      queryKey: ["admin-activity-24h"],
      queryFn: () =>
        api.listAdminActivity({
          since: new Date(Date.now() - 24 * 3_600_000).toISOString(),
          limit: 500,
        }),
    }),
  audit: (pageLimit: number) =>
    infiniteQueryOptions({
      queryKey: ["audit"],
      initialPageParam: "",
      queryFn: ({ pageParam }) =>
        api.listAuditLog({ limit: pageLimit, cursor: pageParam || undefined }),
      getNextPageParam: (last) => last.next_cursor || undefined,
    }),
};

// retryPolicy is the client default. A gateway answer is final: a 4xx names
// a caller mistake and a 5xx names a provider or store fault that a second
// identical request would repeat within the same second. Only a request that
// never reached the gateway earns one more try.
export function retryPolicy(failureCount: number, error: unknown): boolean {
  if (error instanceof api.ApiError) return false;
  return failureCount < 1;
}
