import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { AuthGate, logout, type Identity } from "./auth";
import { api } from "./api";
import { useWorkspaceData } from "./hooks/use-workspace-data";
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
const ConfigPage = lazy(() => import("./pages/ConfigPage").then((module) => ({ default: module.ConfigPage })));
const Dashboard = lazy(() => import("./pages/Dashboard").then((module) => ({ default: module.Dashboard })));
const IndexPage = lazy(() => import("./pages/IndexPage").then((module) => ({ default: module.IndexPage })));
const SearchPage = lazy(() => import("./pages/SearchPage").then((module) => ({ default: module.SearchPage })));
const SourcesPage = lazy(() => import("./pages/SourcesPage").then((module) => ({ default: module.SourcesPage })));
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

const AccessPage = lazy(() =>
  import("./pages/AccessPage").then((module) => ({
    default: module.AccessPage,
  })),
);

export default function App() {
  return <Suspense fallback={<p role="status">加载中…</p>}><AuthGate>{(identity) => <Workspace identity={identity} />}</AuthGate></Suspense>;
}

function Workspace({ identity }: { identity: Identity | null }) {
  const admin = identity === null || identity.admin;
  const [page, setPage] = useState<Page>(admin ? "dashboard" : "search");
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
  useEffect(() => {
    if (!admin && page !== "search" && page !== "sources" && page !== "access")
      setPage("search");
  }, [admin, page]);

  const onLoadError = useCallback((error: Error) => setNotice({ kind: "error", text: error.message }), []);
  const { status, projects, sources, runs, load } = useWorkspaceData(`${identity?.subject ?? "local"}:${admin}`, onLoadError);

  useEffect(() => {
    if (page === "config" && !config)
      api
        .config()
        .then((current) => {
          setConfig(current);
          setRaw(current.raw);
          setActions(current.actions ?? []);
        })
        .catch((error) => setNotice({ kind: "error", text: error.message }));
  }, [page, config]);

  const search = async (filters: {
    project: string;
    mount: string;
    kind: string;
    mode: string;
  }) => {
    if (!query.trim()) return;
    setSearching(true);
    try {
      const params = new URLSearchParams({ q: query });
      if (filters.project) params.set("project", filters.project);
      if (filters.mount) params.set("mount", filters.mount);
      if (filters.kind) params.set("kind", filters.kind);
      if (filters.mode !== "keyword") params.set("mode", filters.mode);
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
      setActions(result.actions ?? []);
      setNotice({
        kind: result.valid ? "success" : "error",
        text: !result.valid
          ? "配置存在冲突，请检查变更计划"
          : (result.restartRequired ?? []).length
            ? `配置校验通过，以下配置保存后需重启：${(result.restartRequired ?? []).join("、")}`
            : (result.actions ?? []).length
              ? `配置有效，检测到 ${(result.actions ?? []).length} 项变更`
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
      setActions(current.actions ?? []);
      setNotice({
        kind: "success",
        text: (current.restartRequired ?? []).length
          ? `配置已保存，来源配置已应用；以下配置需重启：${(current.restartRequired ?? []).join("、")}`
          : "配置已保存并应用",
      });
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
          admin={admin}
          authenticated={identity !== null}
        />
        <SidebarInset>
          <SiteHeader
            title={title}
            onRefresh={() => load()}
            onLogout={
              identity
                ? () =>
                    logout().catch((error) =>
                      setNotice({ kind: "error", text: error.message }),
                    )
                : undefined
            }
          />
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
                    embeddingEnabled={status?.embeddingEnabled === true}
                    setQuery={setQuery}
                    hits={hits}
                    projects={projects}
                    sources={sources}
                    loading={searching}
                    onSearch={search}
                    onSelect={selectMemory}
                    requireProject={identity !== null}
                  />
                )}
                {page === "sources" && (
                  <SourcesPage
                    sources={sources}
                    runs={runs}
                    busy={busy}
                    onSync={sync}
                    canSyncAll={admin}
                    canUpload={(source) =>
                      source.writer === (identity?.subject ?? "local") &&
                      (admin ||
                        (identity?.uploadSources ?? []).includes(source.id))
                    }
                    onUploaded={(pending) => {
                      setNotice({
                        kind: "success",
                        text: pending
                          ? "批次已提交，索引等待投递"
                          : "批次已提交",
                      });
                      void load(true);
                    }}
                    canSync={(source) =>
                      source.driver !== "upload" &&
                      (admin ||
                        (identity?.syncProjects ?? []).includes(
                          source.projectId ?? "",
                        ) ||
                        (identity?.syncSources ?? []).includes(source.id))
                    }
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
                {page === "access" && identity && (
                  <Suspense
                    fallback={
                      <p className="py-6 text-sm text-muted-foreground">
                        加载中…
                      </p>
                    }
                  >
                    <AccessPage
                      identity={identity}
                      projects={projects}
                      sources={sources}
                    />
                  </Suspense>
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
