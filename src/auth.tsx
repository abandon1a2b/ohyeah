import { useEffect, useState, type ReactNode } from "react";
import { LogIn } from "lucide-react";
import { Button } from "@/components/ui/button";

export type Identity = {
  subject: string;
  name?: string;
  admin: boolean;
  projects?: string[];
  syncProjects?: string[];
  syncSources?: string[];
  uploadSources?: string[];
  csrf?: string;
  tokenTTLSeconds?: number;
  credential?: { id: string; kind: string };
};
let csrf = "";
export function csrfHeaders(): Record<string, string> {
  return csrf ? { "X-CSRF-Token": csrf } : {};
}

export function AuthGate({
  children,
}: {
  children: (identity: Identity | null) => ReactNode;
}) {
  const [state, setState] = useState<{
    ready: boolean;
    enabled: boolean;
    identity: Identity | null;
    error?: string;
  }>({ ready: false, enabled: false, identity: null });
  useEffect(() => {
    let active = true;
    async function load() {
      const response = await fetch("/auth/info");
      if (!response.ok) throw new Error("身份服务不可用");
      const info = await response.json();
      if (!info.enabled) {
        if (active) setState({ ready: true, enabled: false, identity: null });
        return;
      }
      const me = await fetch("/auth/me");
      if (me.status === 401) {
        if (active) setState({ ready: true, enabled: true, identity: null });
        return;
      }
      if (!me.ok)
        throw new Error(
          me.status === 403
            ? "当前账号未获授权，请联系管理员"
            : "身份服务不可用",
        );
      const identity: Identity = await me.json();
      csrf = identity.csrf ?? "";
      if (active) setState({ ready: true, enabled: true, identity });
    }
    load().catch((error) => {
      if (active)
        setState({
          ready: true,
          enabled: true,
          identity: null,
          error: error.message,
        });
    });
    const timer = window.setInterval(() => {
      void load().catch((error) => {
        if (active)
          setState({
            ready: true,
            enabled: true,
            identity: null,
            error: error.message,
          });
      });
    }, 30000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);
  if (!state.ready)
    return (
      <main className="flex min-h-screen items-center justify-center">
        <p>正在连接…</p>
      </main>
    );
  if (state.enabled && !state.identity)
    return (
      <main className="mx-auto flex min-h-screen max-w-md flex-col justify-center gap-6 px-6">
        <h1 className="text-2xl font-semibold">ohyeah</h1>
        {state.error && <p role="alert">{state.error}</p>}
        <Button asChild>
          <a href="/auth/login">
            <LogIn />
            登录
          </a>
        </Button>
      </main>
    );
  return children(state.identity);
}

export async function logout() {
  const response = await fetch("/auth/logout", {
    method: "POST",
    headers: csrfHeaders(),
  });
  if (!response.ok) throw new Error("退出失败，请重试");
  window.location.assign("/");
}
