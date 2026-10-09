import { useState } from "react";
import { ChevronRight, Search } from "lucide-react";
import { Badge, Empty, Spinner } from "../components";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { Project, SearchHit, Source } from "../types";

export function SearchPage({
  query,
  embeddingEnabled,
  setQuery,
  hits,
  projects,
  sources,
  loading,
  onSearch,
  onSelect,
}: {
  query: string;
  embeddingEnabled: boolean;
  setQuery: (v: string) => void;
  hits: SearchHit[];
  projects: Project[];
  sources: Source[];
  loading: boolean;
  onSearch: (filters: { project: string; mount: string; kind: string; mode: string }) => void;
  onSelect: (id: string) => void;
}) {
  const [project, setProject] = useState("all"),
    [mount, setMount] = useState("all"),
    [kind, setKind] = useState("all"),
    [mode, setMode] = useState("keyword");
  const filters = {
    project: project === "all" ? "" : project,
    mount: mount === "all" ? "" : mount,
    kind: kind === "all" ? "" : kind,
    mode: embeddingEnabled ? mode : "keyword",
  };
  const visibleSources = sources.filter(
    (source) =>
      source.enabled && (project === "all" || source.projectId === project),
  );
  return (
    <div className="flex flex-col gap-6">
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          onSearch(filters);
        }}
      >
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="h-10 pl-9"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索工作记忆"
            aria-label="搜索工作记忆"
          />
        </div>
        <Button type="submit" disabled={loading || !query.trim()}>
          {loading ? <Spinner label="搜索中" /> : "搜索"}
        </Button>
      </form>
      {embeddingEnabled && <ToggleGroup type="single" variant="outline" value={mode} onValueChange={(value) => value && setMode(value)} aria-label="检索模式" className="self-start">
        <ToggleGroupItem value="keyword">关键词</ToggleGroupItem>
        <ToggleGroupItem value="hybrid">混合</ToggleGroupItem>
        <ToggleGroupItem value="semantic">语义</ToggleGroupItem>
      </ToggleGroup>}
      <FieldGroup className="grid gap-3 sm:grid-cols-3">
        <Field>
          <FieldLabel>项目</FieldLabel>
          <Select
            value={project}
            onValueChange={(value) => {
              setProject(value);
              setMount("all");
            }}
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder="全部项目" />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="all">全部项目</SelectItem>
                {projects.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.id}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel>挂载点</FieldLabel>
          <Select value={mount} onValueChange={setMount}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="全部挂载点" />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="all">全部挂载点</SelectItem>
                {visibleSources.map((source) => (
                  <SelectItem key={source.id} value={source.id}>
                    {source.mountName || source.id}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel>类型</FieldLabel>
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="全部类型" />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="all">全部类型</SelectItem>
                {[
                  "conclusion",
                  "decision",
                  "correction",
                  "verification",
                  "todo",
                  "document",
                ].map((value) => (
                  <SelectItem key={value} value={value}>
                    {value}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
      </FieldGroup>
      {hits.length === 0 ? (
        <Empty
          title="输入关键词开始检索"
          detail="可以按项目、挂载点和类型缩小范围"
        />
      ) : (
        <section>
          <div className="mb-2 text-xs text-muted-foreground">
            {hits.length} 条结果
          </div>
          <div className="divide-y border-y">
            {hits.map((hit) => (
              <button
                type="button"
                className="flex w-full items-start gap-4 px-2 py-4 text-left transition-colors hover:bg-muted/50"
                key={hit.unit.id}
                onClick={() => onSelect(hit.unit.id)}
              >
                <div className="min-w-0 flex-1">
                  <div className="mb-2 flex flex-wrap items-center gap-2">
                    <Badge
                      tone={
                        hit.unit.kind === "correction" ? "warning" : "neutral"
                      }
                    >
                      {hit.unit.kind}
                    </Badge>
                    <span className="font-medium">{hit.unit.title}</span>
                  </div>
                  <p className="line-clamp-2 text-sm leading-6 text-muted-foreground">
                    {hit.unit.content}
                  </p>
                  <div className="mt-2 text-xs text-muted-foreground">
                    {hit.unit.sourceId} ·{" "}
                    {new Date(hit.unit.occurredAt).toLocaleString()}
                  </div>
                </div>
                <ChevronRight className="mt-1 size-4 shrink-0 text-muted-foreground" />
              </button>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
