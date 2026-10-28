import type { UploadReceipt } from "./generated/contracts";
import type {
  ConfigData,
  Doctor,
  MemoryRecord,
  Project,
  SearchHit,
  Source,
  Status,
  SyncRun,
} from "./types";
import { createClient } from "./http";

const request = createClient("/api/v1");

export const api = {
  status: (signal?: AbortSignal) => request<Status>("/status", { signal }),
  projects: (signal?: AbortSignal) => request<Project[]>("/projects", { signal }),
  sources: (signal?: AbortSignal) => request<Source[]>("/sources", { signal }),
  runs: (limit = 200, source?: string, signal?: AbortSignal) =>
    request<SyncRun[]>(
      `/sync/runs?limit=${limit}${source ? `&source=${encodeURIComponent(source)}` : ""}`,
      { signal },
    ),
  sync: (sourceId?: string) =>
    request<SyncRun | SyncRun[]>("/sync", {
      method: "POST",
      body: JSON.stringify({ sourceId }),
    }),
  upload: (batch: unknown) =>
    request<UploadReceipt>("/uploads", { method: "POST", body: JSON.stringify(batch) }),
  search: (params: URLSearchParams) =>
    request<SearchHit[]>(`/search?${params}`),
  memory: (id: string) =>
    request<MemoryRecord>(`/memories/${encodeURIComponent(id)}`),
  doctor: () => request<Doctor>("/index/doctor"),
  rebuild: () =>
    request<{ rebuilt: boolean; documents: number }>("/index/rebuild", {
      method: "POST",
    }),
  config: () => request<ConfigData>("/config"),
  validateConfig: (raw: string) =>
    request<ConfigData>("/config/validate", {
      method: "POST",
      body: JSON.stringify({ raw }),
    }),
  saveConfig: (raw: string, expectedHash: string) =>
    request<ConfigData>(
      "/config",
      { method: "PUT", body: JSON.stringify({ raw, expectedHash }) },
    ),
};
