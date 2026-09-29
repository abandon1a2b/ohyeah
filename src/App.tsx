import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { Badge, Notice } from "./components";
import { AppSidebar, navigation, type Page } from "@/components/app-sidebar";
import { SiteHeader } from "@/components/site-header";
import { Separator } from "@/components/ui/separator";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { ConfigPage } from "./pages/ConfigPage";
import { Dashboard } from "./pages/Dashboard";
import { IndexPage } from "./pages/IndexPage";
import { SearchPage } from "./pages/SearchPage";
import { SourcesPage } from "./pages/SourcesPage";
import type {
  ConfigData,
  MemoryRecord,
  Project,
  RegistryAction,
  SearchHit,
  Source,
  Status,
  SyncRun,
} from "./types";

export default function App() {
  const [page, setPage] = useState<Page>("dashboard");
  const [status, setStatus] = useState<Status | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [sources, setSources] = useState<Source[]>([]);
  const [runs, setRuns] = useState<SyncRun[]>([]);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [memory, setMemory] = useState<MemoryRecord | null>(null);
  const [searching, setSearching] = useState(false);
  const [config, setConfig] = useState<ConfigData | null>(null);
  const [raw, setRaw] = useState("");
  const [actions, setActions] = useState<RegistryAction[]>([]);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState<{
    kind: "success" | "error";
    text: string;
  } | null>(null);
  const title = navigation.find((item) => item.id === page)?.label ?? "详情";

  const load = useCallback(async (silent = false) => {
    try {
      const [s, p, src, r] = await Promise.all([
        api.status(),
        api.projects(),
        api.sources(),
        api.runs(),
      ]);
      setStatus(s);
      setProjects(p);
      setSources(src);
      setRuns(r);
    } catch (error) {
      if (!silent) setNotice({ kind: "error", text: (error as Error).message });
    }
  }, []);

  useEffect(() => {
    load();
    const timer = setInterval(() => load(true), 10000);
    return () => clearInterval(timer);
  }, [load]);

  useEffect(() => {
    if (page === "config" && !config)
      api
        .config()
        .then((current) => {
          setConfig(current);
          setRaw(current.raw);
          setActions(current.actions);
        })
        .catch((error) => setNotice({ kind: "error", text: error.message }));
  }, [page, config]);

  const search = async (filters: {
    project: string;
    mount: string;
    kind: string;
  }) => {
    if (!query.trim()) return;
    setSearching(true);
    try {
      const params = new URLSearchParams({ q: query });
      if (filters.project) params.set("project", filters.project);
      if (filters.mount) params.set("mount", filters.mount);
      if (filters.kind) params.set("kind", filters.kind);
      setHits(await api.search(params));
    } catch (error) {
      setNotice({ kind: "error", text: (error as Error).message });
    } finally {
      setSearching(false);
    }
  };

  const selectMemory = async (id: string) => {
    try {
      setMemory(await api.memory(id));
    } catch (error) {
      setNotice({ kind: "error", text: (error as Error).message });
    }
  };

  const sync = async (id?: string) => {
    setBusy(id || "all");
    try {
      await api.sync(id);
      setNotice({
        kind: "success",
        text: id ? `已同步 ${id}` : "全部数据来源同步完成",
      });
      await load(true);
    } catch (error) {
      setNotice({ kind: "error", text: (error as Error).message });
    } finally {
      setBusy("");
    }
  };

  const validate = async () => {
    setBusy("validate");
    try {
      const result = await api.validateConfig(raw);
      setActions(result.actions);
      setNotice({
        kind: "success",
        text: result.actions.length
          ? `配置有效，检测到 ${result.actions.length} 项变更`
          : "配置有效，运行状态已一致",
      });
    } catch (error) {
      setNotice({ kind: "error", text: (error as Error).message });
    } finally {
      setBusy("");
    }
  };

  const save = async () => {
    if (!config) return;
    setBusy("save");
    try {
      await api.saveConfig(raw, config.hash);
      const current = await api.config();
      setConfig(current);
      setRaw(current.raw);
      setActions(current.actions);
      setNotice({ kind: "success", text: "配置已原子保存并自动应用" });
      await load(true);
    } catch (error) {
      setNotice({ kind: "error", text: (error as Error).message });
    } finally {
      setBusy("");
    }
  };

  const rebuild = async () => {
    setBusy("rebuild");
    try {
      const result = await api.rebuild();
      setNotice({
        kind: "success",
        text: `已重建 ${result.documents} 条索引记录`,
      });
      await load(true);
    } catch (error) {
      setNotice({ kind: "error", text: (error as Error).message });
    } finally {
      setBusy("");
    }
  };

  const changePage = (next: Page) => {
    setPage(next);
    setNotice(null);
  };

  return (
    <TooltipProvider>
      <SidebarProvider
        style={
          {
            "--sidebar-width": "calc(var(--spacing) * 72)",
            "--header-height": "calc(var(--spacing) * 12)",
          } as React.CSSProperties
        }
      >
        <AppSidebar
          variant="inset"
          page={page}
          backendAvailable={status?.backend === "available"}
          onNavigate={changePage}
        />
        <SidebarInset>
          <SiteHeader title={title} onRefresh={() => load()} />
          <div className="flex flex-1 flex-col">
            <div className="@container/main flex flex-1 flex-col gap-2">
              <div
                className={cn(
                  "flex flex-col gap-4 py-4 md:gap-6 md:py-6",
                  page !== "dashboard" && "px-4 lg:px-6",
                )}
              >
                {notice && (
                  <div
                    className={
                      page === "dashboard" ? "px-4 lg:px-6" : undefined
                    }
                  >
                    <Notice kind={notice.kind} close={() => setNotice(null)}>
                      {notice.text}
                    </Notice>
                  </div>
                )}
                {page === "dashboard" && (
                  <Dashboard status={status} runs={runs} />
                )}
                {page === "search" && (
                  <SearchPage
                    query={query}
                    setQuery={setQuery}
                    hits={hits}
                    projects={projects}
                    sources={sources}
                    loading={searching}
                    onSearch={search}
                    onSelect={selectMemory}
                  />
                )}
                {page === "sources" && (
                  <SourcesPage
                    sources={sources}
                    runs={runs}
                    busy={busy}
                    onSync={sync}
                  />
                )}
                {page === "config" && (
                  <ConfigPage
                    data={config}
                    sources={sources}
                    raw={raw}
                    setRaw={setRaw}
                    actions={actions}
                    busy={busy}
                    onValidate={validate}
                    onSave={save}
                  />
                )}
                {page === "index" && (
                  <IndexPage
                    status={status}
                    busy={busy === "rebuild"}
                    onRebuild={rebuild}
                  />
                )}
              </div>
            </div>
          </div>
        </SidebarInset>
        <MemoryDrawer record={memory} close={() => setMemory(null)} />
      </SidebarProvider>
    </TooltipProvider>
  );
}

function MemoryDrawer({
  record,
  close,
}: {
  record: MemoryRecord | null;
  close: () => void;
}) {
  const m = record?.unit;
  return (
    <Sheet
      open={!!record}
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="border-b pb-5">
          <div className="mb-2">
            <Badge tone={m?.kind === "correction" ? "warning" : "neutral"}>
              {m?.kind}
            </Badge>
          </div>
          <SheetTitle className="text-left text-lg leading-snug">
            {m?.title}
          </SheetTitle>
          <SheetDescription className="text-left">
            {m && new Date(m.occurredAt).toLocaleString()} · {m?.status}
          </SheetDescription>
        </SheetHeader>
        {m && (
          <div className="flex flex-col gap-7 px-5 pb-8">
            <p className="whitespace-pre-wrap text-sm leading-7">{m.content}</p>
            <Separator />
            <dl className="grid gap-4 text-sm">
              <Meta label="来源" value={`${m.sourceType} · ${m.sourceId}`} />
              {m.path && <Meta label="路径" value={m.path} />}
              {m.threadId && <Meta label="会话" value={m.threadId} />}
              {Object.entries(record?.reference || {}).map(([key, value]) => (
                <Meta key={key} label={key} value={value} />
              ))}
            </dl>
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="mb-1 text-xs text-muted-foreground">{label}</dt>
      <dd className="break-all">{value}</dd>
    </div>
  );
}
