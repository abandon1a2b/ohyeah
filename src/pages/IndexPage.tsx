import { Database, RotateCcw } from "lucide-react";
import { Badge, Spinner } from "../components";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import type { Status } from "../types";

export function IndexPage({
  status,
  busy,
  onRebuild,
}: {
  status: Status | null;
  busy: boolean;
  onRebuild: () => void;
}) {
  const doctor = status?.doctor;
  return (
    <div className="flex flex-col gap-7">
      <div>
        <h2 className="text-lg font-semibold">索引状态</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          SQLite 与 Meilisearch 记录对照
        </p>
      </div>
      <section className="grid gap-3 sm:grid-cols-3">
        <Card className="gap-3 py-4 shadow-none">
          <CardHeader className="px-4">
            <CardTitle className="text-xs text-muted-foreground">
              SQLite
            </CardTitle>
          </CardHeader>
          <CardContent className="px-4 text-2xl font-semibold tabular-nums">
            {doctor?.sqliteCount ?? 0}
          </CardContent>
        </Card>
        <Card className="gap-3 py-4 shadow-none">
          <CardHeader className="px-4">
            <CardTitle className="text-xs text-muted-foreground">
              Meilisearch
            </CardTitle>
          </CardHeader>
          <CardContent className="px-4 text-2xl font-semibold tabular-nums">
            {doctor?.meilisearchCount ?? 0}
          </CardContent>
        </Card>
        <Card className="gap-3 py-4 shadow-none">
          <CardHeader className="px-4">
            <CardTitle className="text-xs text-muted-foreground">
              一致性
            </CardTitle>
          </CardHeader>
          <CardContent className="px-4">
            <Badge tone={doctor?.consistent ? "success" : "danger"}>
              {doctor?.consistent ? "一致" : "不一致"}
            </Badge>
          </CardContent>
        </Card>
      </section>
      <Separator />
      <section className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex max-w-xl gap-3">
          <Database className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <div>
            <h3 className="text-sm font-medium">重建搜索索引</h3>
            <p className="mt-1 text-sm leading-6 text-muted-foreground">
              从 SQLite 重放全部记忆单元，修复搜索索引与本地数据的差异。
            </p>
          </div>
        </div>
        <Button variant="outline" disabled={busy} onClick={onRebuild}>
          {busy ? (
            <Spinner label="重建中" />
          ) : (
            <>
              <RotateCcw data-icon="inline-start" />
              重建索引
            </>
          )}
        </Button>
      </section>
    </div>
  );
}
