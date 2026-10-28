let csrf = "";
export function setCSRF(value: string) { csrf = value; }
export function csrfHeaders(): Record<string, string> {
  return csrf ? { "X-CSRF-Token": csrf } : {};
}
