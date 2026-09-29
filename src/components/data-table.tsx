import { useMemo, useState } from "react";
import { Search } from "lucide-react";
import { Badge, Empty } from "@/components";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { SyncRun } from "@/types";

export function DataTable({ runs }: { runs: SyncRun[] }) {
  const [status, setStatus] = useState("all");
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState("10");
  const visible = useMemo(
    () =>
      runs
        .filter(
          (run) =>
            (status === "all" || run.status === status) &&
            run.sourceId.toLowerCase().includes(query.toLowerCase()),
        )
        .slice(0, Number(limit)),
    [runs, status, query, limit],
  );

  return (
    <section className="flex flex-col gap-4 px-4 lg:px-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold">同步记录</h2>
          <p className="text-sm text-muted-foreground">
            按时间查看数据来源的运行结果
          </p>
        </div>
        <span className="text-xs text-muted-foreground">
          显示 {visible.length} / {runs.length}
        </span>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs value={status} onValueChange={setStatus}>
          <TabsList>
            <TabsTrigger value="all">全部</TabsTrigger>
            <TabsTrigger value="succeeded">成功</TabsTrigger>
            <TabsTrigger value="running">运行中</TabsTrigger>
            <TabsTrigger value="failed">失败</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="flex items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              className="w-44 pl-8 sm:w-56"
              placeholder="筛选来源"
              aria-label="筛选来源"
            />
          </div>
          <Select value={limit} onValueChange={setLimit}>
            <SelectTrigger className="w-20" aria-label="显示条数">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="10">10 条</SelectItem>
                <SelectItem value="20">20 条</SelectItem>
                <SelectItem value="50">50 条</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      </div>
      {visible.length === 0 ? (
        <Empty title="没有符合条件的同步记录" />
      ) : (
        <>
          <div className="divide-y rounded-md border md:hidden">
            {visible.map((run) => (
              <div key={run.id} className="flex flex-col gap-2 p-3">
                <div className="break-words text-sm font-medium">
                  {run.sourceId}
                </div>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <Badge
                    tone={
                      run.status === "succeeded"
                        ? "success"
                        : run.status === "failed"
                          ? "danger"
                          : "warning"
                    }
                  >
                    {run.status}
                  </Badge>
                  <span>{new Date(run.startedAt).toLocaleString()}</span>
                  <span>扫描 {run.scanned}</span>
                  <span>变更 {run.changed}</span>
                  <span>删除 {run.deleted}</span>
                </div>
              </div>
            ))}
          </div>
          <div className="hidden overflow-x-auto rounded-md border md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>数据来源</TableHead>
                  <TableHead>开始时间</TableHead>
                  <TableHead className="text-right">扫描</TableHead>
                  <TableHead className="text-right">变更</TableHead>
                  <TableHead className="text-right">删除</TableHead>
                  <TableHead>状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((run) => (
                  <TableRow key={run.id}>
                    <TableCell className="max-w-64 truncate font-medium">
                      {run.sourceId}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {new Date(run.startedAt).toLocaleString()}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {run.scanned}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {run.changed}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {run.deleted}
                    </TableCell>
                    <TableCell>
                      <Badge
                        tone={
                          run.status === "succeeded"
                            ? "success"
                            : run.status === "failed"
                              ? "danger"
                              : "warning"
                        }
                      >
                        {run.status}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </section>
  );
}
