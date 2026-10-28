import { createClient } from "./http";

import type { Credential, Principal, PrincipalPolicy, AuditEvent } from "./generated/contracts";
export type { Credential, Principal, PrincipalPolicy, AuditEvent } from "./generated/contracts";

const request = createClient("/auth");

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
