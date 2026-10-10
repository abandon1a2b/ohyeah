import { csrfHeaders } from "./auth";

export type Credential = {
  id: string;
  subject: string;
  name: string;
  kind: string;
  expiresAt: number;
  createdAt: number;
  lastUsedAt: number;
};
export type Principal = {
  subject: string;
  name: string;
  admin: boolean;
  disabled: boolean;
  projects: string[] | null;
  syncProjects: string[] | null;
  syncSources: string[] | null;
  uploadSources: string[] | null;
};
export type PrincipalPolicy = {
  subject: string;
  version: number;
  principal: Principal | null;
  configured: boolean;
};
export type AuditEvent = {
  id: number;
  occurredAt: number;
  subject: string;
  credentialId: string;
  action: string;
  resource: string;
  status: number;
};

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/auth${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...csrfHeaders(),
      ...init?.headers,
    },
  });
  const body = await response.json().catch(() => ({}));
  if (response.status === 401) window.location.assign("/auth/login");
  if (!response.ok)
    throw new Error(body.error || `请求失败 (${response.status})`);
  return body as T;
}

export const securityAPI = {
  tokens: (all: boolean) => request<Credential[]>(`/tokens?all=${all}`),
  issue: (subject: string, name: string, expiresIn: number) =>
    request<{ token: string; credential: Credential }>("/tokens", {
      method: "POST",
      body: JSON.stringify({ subject, name, expiresIn }),
    }),
  revoke: (id: string) =>
    request(`/tokens/${encodeURIComponent(id)}`, { method: "DELETE" }),
  rotate: (id: string) =>
    request<{ token: string; credential: Credential }>(
      `/tokens/${encodeURIComponent(id)}/rotate`,
      { method: "POST" },
    ),
  principals: () => request<PrincipalPolicy[]>("/principals"),
  principal: (subject: string) =>
    request<PrincipalPolicy>(`/principals/${encodeURIComponent(subject)}`),
  save: (principal: Principal, expectedVersion: number) =>
    request<PrincipalPolicy>(
      `/principals/${encodeURIComponent(principal.subject)}`,
      { method: "PUT", body: JSON.stringify({ principal, expectedVersion }) },
    ),
  reset: (subject: string, version: number) =>
    request(`/principals/${encodeURIComponent(subject)}?version=${version}`, {
      method: "DELETE",
    }),
  audit: (all: boolean, before = 0) =>
    request<AuditEvent[]>(`/audit?all=${all}&before=${before}&limit=50`),
};
