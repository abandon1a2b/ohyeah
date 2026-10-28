import { csrfHeaders } from "./csrf";

export class HTTPError extends Error {
  constructor(message: string, readonly status: number, readonly body: unknown) {
    super(message);
  }
}

export function createClient(prefix: string) {
  return async function request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    if (init?.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    for (const [key, value] of Object.entries(csrfHeaders())) headers.set(key, value);
    const response = await fetch(`${prefix}${path}`, { ...init, headers });
    if (response.status === 401) window.location.assign("/auth/login");
    if (response.status === 204) return undefined as T;
    const body = await response.json();
    if (!response.ok || (response.status === 207 && body.error)) throw new HTTPError(body.error || `请求失败 (${response.status})`, response.status, body);
    return body as T;
  };
}
