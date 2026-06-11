import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiGet, apiPost, apiPostConfirm } from "./client";

const get = (path: string) => () => apiGet(path);

export const useMe = () => useQuery({ queryKey: ["me"], queryFn: get("/v1/me") });
export const useSystem = () =>
  useQuery({ queryKey: ["system"], queryFn: get("/v1/system"), refetchInterval: 5000 });
export const useAppsState = () =>
  useQuery({ queryKey: ["apps-state"], queryFn: get("/v1/apps/state"), refetchInterval: 8000 });
export const usePolicies = () => useQuery({ queryKey: ["policies"], queryFn: get("/v1/policies") });
export const useStorage = () => useQuery({ queryKey: ["storage"], queryFn: get("/v1/storage") });
export const useSecrets = () =>
  useQuery({ queryKey: ["secrets"], queryFn: get("/v1/secrets/status") });
export const useBackups = () =>
  useQuery({ queryKey: ["backups"], queryFn: get("/v1/backups"), refetchInterval: 10000 });
export const useHealth = () =>
  useQuery({ queryKey: ["health"], queryFn: get("/v1/health/apps"), refetchInterval: 8000 });
export const useObservability = () =>
  useQuery({ queryKey: ["observability"], queryFn: get("/v1/observability"), refetchInterval: 6000 });
export const useLibrary = () => useQuery({ queryKey: ["library"], queryFn: get("/v1/library") });
export const useChanges = () =>
  useQuery({ queryKey: ["changes"], queryFn: get("/v1/changes"), refetchInterval: 10000 });
export const usePlatform = () => useQuery({ queryKey: ["platform"], queryFn: get("/v1/platform") });
// Drift vs origin/main. The server caches ls-remote for 15min, so the 60s
// refetch is cheap (it almost always hits the cache).
export const useDrift = () =>
  useQuery({ queryKey: ["drift"], queryFn: get("/v1/drift"), refetchInterval: 60000 });
export const useUpdates = () =>
  useQuery({ queryKey: ["updates"], queryFn: get("/v1/updates"), refetchInterval: 60000 });
export const useGenerations = () =>
  useQuery({ queryKey: ["generations"], queryFn: get("/v1/generations") });
export const useSystemSecrets = () =>
  useQuery({ queryKey: ["system-secrets"], queryFn: get("/v1/secrets/system") });
export const useStorageOrphans = () =>
  useQuery({ queryKey: ["storage-orphans"], queryFn: get("/v1/storage/orphans") });
export const useAudit = (opts: { limit?: number; op?: string; result?: string; includeUi?: boolean } = {}) => {
  const { limit = 200, op = "", result = "", includeUi = false } = opts;
  const qs = new URLSearchParams({ limit: String(limit) });
  if (op) qs.set("op", op);
  if (result) qs.set("result", result);
  if (includeUi) qs.set("include_ui", "true");
  const q = qs.toString();
  return useQuery({ queryKey: ["audit", { limit, op, result, includeUi }], queryFn: get(`/v1/audit?${q}`), refetchInterval: 15000 });
};

export function usePost(invalidate?: string[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { path: string; payload?: any }) => apiPost(args.path, args.payload),
    onSuccess: () => {
      (invalidate || []).forEach((k) => qc.invalidateQueries({ queryKey: [k] }));
    },
  });
}

// Like usePost but transparently handles the control-api double-confirm
// challenge: a 409 { confirm_id } is surfaced to args.confirm(message); if the
// operator accepts, the same payload is re-POSTed once with confirm_id added.
export function usePostConfirm(invalidate?: string[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { path: string; payload?: any; confirm: (message: string) => boolean | Promise<boolean> }) =>
      apiPostConfirm(args.path, args.payload, args.confirm),
    onSuccess: () => {
      (invalidate || []).forEach((k) => qc.invalidateQueries({ queryKey: [k] }));
    },
  });
}
