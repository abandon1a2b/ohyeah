import { FolderSync, RefreshCw } from "lucide-react";
import { Badge, Empty, Spinner } from "../components";
import { Button } from "@/components/ui/button";
import { finishLabel } from "@/sync-run-time";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Source, SyncRun } from "../types";

export function SourcesPage({
  sources,
  runs,
  busy,
  onSync,
}: {
  sources: Source[];
  runs: SyncRun[];
  busy: string;
  onSync: (id?: string) => void;
}) {
  const latest = new Map<string, SyncRun>();
  for (const run of runs)
    if (!latest.has(run.sourceId)) latest.set(run.sourceId, run);
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">数据来源</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            已注册的数据挂载与最近同步状态
          </p>
        </div>
        <Button disabled={!!busy} onClick={() => onSync()}>
          {busy === "all" ? (
            <Spinner label="同步全部" />
          ) : (
            <>
              <RefreshCw data-icon="inline-start" />
              同步全部
            </>
          )}
        </Button>
      </div>
      {sources.length === 0 ? (
        <Empty
          title="没有已注册的数据来源"
          detail="请先在配置中添加项目和挂载点"
        />
      ) : (
        <>
          <div className="divide-y rounded-md border md:hidden">
            {sources.map((source) => {
              const run = latest.get(source.id);
              return (
                <div className="flex flex-col gap-3 p-3" key={source.id}>
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <div className="break-all text-sm font-medium">
                        {source.id}
                      </div>
                      <div className="mt-1 break-all text-xs text-muted-foreground">
                        {source.path}
                      </div>
                    </div>
                    <Badge tone={source.enabled ? "success" : "neutral"}>
                      {source.enabled ? "启用" : "停用"}
                    </Badge>
                  </div>
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs text-muted-foreground">
                      {run ? `${run.status} · 变更 ${run.changed}` : "尚未同步"}
                    </span>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!!busy || !source.enabled}
                      onClick={() => onSync(source.id)}
                    >
                      {busy === source.id ? <Spinner label="同步中" /> : "同步"}
                    </Button>
                  </div>
                  {run && (
                    <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                      <span>开始 {new Date(run.startedAt).toLocaleString()}</span>
                      <span>结束 {finishLabel(run)}</span>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
          <div className="hidden overflow-x-auto rounded-md border md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>来源</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>最近同步</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sources.map((source) => {
                  const run = latest.get(source.id);
                  return (
                    <TableRow key={source.id}>
                      <TableCell>
                        <div className="flex min-w-44 items-center gap-3">
                          <FolderSync className="size-4 shrink-0 text-muted-foreground" />
                          <div className="min-w-0">
                            <div className="truncate font-medium">
                              {source.id}
                            </div>
                            <div
                              className="max-w-80 truncate text-xs text-muted-foreground"
                              title={source.path}
                            >
                              {source.path}
                            </div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge tone={source.enabled ? "success" : "neutral"}>
                          {source.enabled ? "启用" : "停用"}
                        </Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-sm text-muted-foreground">
                        {run ? (
                          <>
                            <span>
                              {run.status} · 变更 {run.changed}
                            </span>
                            <div className="text-xs">
                              开始 {new Date(run.startedAt).toLocaleString()}
                              <span className="mx-1">·</span>
                              结束 {finishLabel(run)}
                            </div>
                          </>
                        ) : (
                          "尚未同步"
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={!!busy || !source.enabled}
                          onClick={() => onSync(source.id)}
                        >
                          {busy === source.id ? (
                            <Spinner label="同步中" />
                          ) : (
                            "同步"
                          )}
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </div>
  );
}
