import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api";
import type { Project, Source, Status, SyncRun } from "../types";

export function useWorkspaceData(identityKey: string, onError: (error: Error) => void) {
  const [data, setData] = useState<{ status: Status | null; projects: Project[]; sources: Source[]; runs: SyncRun[] }>({
    status: null, projects: [], sources: [], runs: [],
  });
  const active = useRef<AbortController | null>(null);
  const load = useCallback(async (silent = false) => {
    if (silent && active.current) return;
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    const { signal } = controller;
    try {
      const [status, projects, sources, runs] = await Promise.all([
        api.status(signal), api.projects(signal), api.sources(signal), api.runs(200, undefined, signal),
      ]);
      if (!signal.aborted) setData({ status, projects, sources, runs });
    } catch (error) {
      if (!signal.aborted && !silent) onError(error as Error);
    } finally {
      if (active.current === controller) active.current = null;
    }
  }, [onError]);
  useEffect(() => {
    void load();
    const timer = setInterval(() => { if (document.visibilityState === "visible") void load(true); }, 10_000);
    return () => { clearInterval(timer); active.current?.abort(); active.current = null; };
  }, [identityKey, load]);
  return { ...data, load };
}
