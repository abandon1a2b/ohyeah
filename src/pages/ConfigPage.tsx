import { useId, useMemo, useState } from "react";
import { Check, FileCode2, Save, SlidersHorizontal } from "lucide-react";
import { parseDocument } from "yaml";
import { Badge, Empty, Spinner } from "../components";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import type { ConfigData, RegistryAction, Source } from "../types";

type ConfigMap = any;

export function ConfigPage({
  data,
  sources,
  raw,
  setRaw,
  actions,
  busy,
  onValidate,
  onSave,
}: {
  data: ConfigData | null;
  sources: Source[];
  raw: string;
  setRaw: (v: string) => void;
  actions: RegistryAction[];
  busy: string;
  onValidate: () => void;
  onSave: () => void;
}) {
  const [mode, setMode] = useState("form");
  const parsed = useMemo(() => {
    try {
      return (parseDocument(raw).toJS() || {}) as ConfigMap;
    } catch {
      return {};
    }
  }, [raw]);
  const update = (path: (string | number)[], value: unknown) => {
    const document = parseDocument(raw);
    document.setIn(path, value);
    setRaw(document.toString());
  };
  const projects = Object.entries(parsed.projects || {}) as [
    string,
    ConfigMap,
  ][];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">配置管理</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            保存后自动校验并应用
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            disabled={!!busy || !data}
            onClick={onValidate}
          >
            {busy === "validate" ? (
              <Spinner label="校验中" />
            ) : (
              <>
                <Check data-icon="inline-start" />
                校验
              </>
            )}
          </Button>
          <Button disabled={!!busy || !data} onClick={onSave}>
            {busy === "save" ? (
              <Spinner label="保存中" />
            ) : (
              <>
                <Save data-icon="inline-start" />
                保存并应用
              </>
            )}
          </Button>
        </div>
      </div>
      <Tabs value={mode} onValueChange={setMode}>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b pb-3">
          <TabsList>
            <TabsTrigger value="form">
              <SlidersHorizontal data-icon="inline-start" />
              结构化编辑
            </TabsTrigger>
            <TabsTrigger value="yaml">
              <FileCode2 data-icon="inline-start" />
              YAML
            </TabsTrigger>
          </TabsList>
          <code
            className="max-w-full truncate text-xs text-muted-foreground"
            title={data?.path}
          >
            {data?.path}
          </code>
        </div>
        <TabsContent value="form" className="pt-5">
          <StructuredEditor
            config={parsed}
            projects={projects}
            update={update}
          />
        </TabsContent>
        <TabsContent value="yaml" className="pt-5">
          <Textarea
            value={raw}
            onChange={(event) => setRaw(event.target.value)}
            spellCheck={false}
            className="min-h-150 resize-y font-mono text-xs leading-6"
            aria-label="配置 YAML"
          />
        </TabsContent>
      </Tabs>
      <section className="flex flex-col gap-3">
        <h3 className="text-sm font-semibold">待应用变更</h3>
        {actions.length === 0 ? (
          <Empty title="运行配置已与文件一致" />
        ) : (
          <div className="divide-y border-y">
            {actions.map((action, index) => (
              <div
                className="flex items-center gap-3 py-3"
                key={`${action.id}-${index}`}
              >
                <Badge
                  tone={
                    action.type === "conflict"
                      ? "danger"
                      : action.resetCursor
                        ? "warning"
                        : "info"
                  }
                >
                  {action.type}
                </Badge>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">
                    {action.id}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {action.reason}
                  </div>
                </div>
                {action.resetCursor && <Badge tone="warning">重置游标</Badge>}
              </div>
            ))}
          </div>
        )}
      </section>
      <section className="flex flex-col gap-3">
        <h3 className="text-sm font-semibold">运行时挂载</h3>
        {sources.length === 0 ? (
          <Empty title="没有运行时挂载" />
        ) : (
          <div className="divide-y border-y">
            {sources.map((source) => (
              <div className="flex items-center gap-3 py-3" key={source.id}>
                <span
                  className={`size-2 rounded-full ${source.enabled ? "bg-emerald-500" : "bg-muted-foreground/40"}`}
                />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">
                    {source.id}
                  </div>
                  <div
                    className="truncate text-xs text-muted-foreground"
                    title={source.path}
                  >
                    {source.path}
                  </div>
                </div>
                <Badge tone={source.enabled ? "success" : "neutral"}>
                  {source.enabled ? "启用" : "停用"}
                </Badge>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function StructuredEditor({
  config,
  projects,
  update,
}: {
  config: ConfigMap;
  projects: [string, ConfigMap][];
  update: (path: (string | number)[], value: unknown) => void;
}) {
  return (
    <div className="flex flex-col gap-7">
      <section>
        <h3 className="mb-4 text-sm font-semibold">服务设置</h3>
        <FieldGroup className="grid gap-4 sm:grid-cols-2">
          <TextField
            label="监听地址"
            value={config.server?.address || "127.0.0.1:8787"}
            onChange={(value) => update(["server", "address"], value)}
          />
          <TextField
            label="定时同步间隔"
            value={config.sync?.reconcile_interval || "15m"}
            onChange={(value) => update(["sync", "reconcile_interval"], value)}
          />
          <TextField
            label="文件变化防抖"
            value={config.sync?.debounce || "2s"}
            onChange={(value) => update(["sync", "debounce"], value)}
          />
          <TextField
            label="Collector 默认超时"
            value={config.sync?.collector_timeout || "5m"}
            onChange={(value) => update(["sync", "collector_timeout"], value)}
          />
        </FieldGroup>
      </section>
      <Separator />
      {projects.map(([projectId, project]) => (
        <section className="flex flex-col gap-5" key={projectId}>
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="text-xs text-muted-foreground">项目</div>
              <h3 className="font-semibold">{projectId}</h3>
            </div>
            <Toggle
              checked={project.enabled !== false}
              label="启用项目"
              onChange={(value) =>
                update(["projects", projectId, "enabled"], value)
              }
            />
          </div>
          <TextField
            label="Workspace"
            value={project.workspace || ""}
            onChange={(value) =>
              update(["projects", projectId, "workspace"], value)
            }
          />
          {Object.entries(project.types || {}).map(
            ([typeId, type]: [string, ConfigMap]) => (
              <div className="flex flex-col gap-5 border-l-2 pl-4" key={typeId}>
                <div className="flex items-center justify-between gap-3">
                  <h4 className="text-sm font-semibold">{typeId}</h4>
                  <Toggle
                    checked={type.enabled !== false}
                    label="启用类型"
                    onChange={(value) =>
                      update(
                        ["projects", projectId, "types", typeId, "enabled"],
                        value,
                      )
                    }
                  />
                </div>
                <FieldGroup className="grid gap-4 sm:grid-cols-3">
                  <TextField
                    label="Collector command"
                    value={type.collector?.command || ""}
                    onChange={(value) =>
                      update(
                        [
                          "projects",
                          projectId,
                          "types",
                          typeId,
                          "collector",
                          "command",
                        ],
                        value,
                      )
                    }
                  />
                  <TextField
                    label="Revision"
                    type="number"
                    value={type.collector?.revision ?? 1}
                    onChange={(value) =>
                      update(
                        [
                          "projects",
                          projectId,
                          "types",
                          typeId,
                          "collector",
                          "revision",
                        ],
                        Number(value),
                      )
                    }
                  />
                  <TextField
                    label="Timeout"
                    value={type.collector?.timeout || ""}
                    onChange={(value) =>
                      update(
                        [
                          "projects",
                          projectId,
                          "types",
                          typeId,
                          "collector",
                          "timeout",
                        ],
                        value,
                      )
                    }
                  />
                </FieldGroup>
                <div className="divide-y border-y">
                  {Object.entries(type.mounts || {}).map(
                    ([mountId, mount]: [string, ConfigMap]) => (
                      <div className="flex flex-col gap-4 py-5" key={mountId}>
                        <div className="flex items-center justify-between gap-3">
                          <h5 className="text-sm font-medium">{mountId}</h5>
                          <Toggle
                            checked={mount.enabled !== false}
                            label="启用挂载"
                            onChange={(value) =>
                              update(
                                [
                                  "projects",
                                  projectId,
                                  "types",
                                  typeId,
                                  "mounts",
                                  mountId,
                                  "enabled",
                                ],
                                value,
                              )
                            }
                          />
                        </div>
                        <FieldGroup className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
                          <TextField
                            label="Root"
                            value={mount.root || ""}
                            onChange={(value) =>
                              update(
                                [
                                  "projects",
                                  projectId,
                                  "types",
                                  typeId,
                                  "mounts",
                                  mountId,
                                  "root",
                                ],
                                value,
                              )
                            }
                          />
                          <TextField
                            label="Revision"
                            type="number"
                            value={mount.revision ?? 1}
                            onChange={(value) =>
                              update(
                                [
                                  "projects",
                                  projectId,
                                  "types",
                                  typeId,
                                  "mounts",
                                  mountId,
                                  "revision",
                                ],
                                Number(value),
                              )
                            }
                          />
                          <TextField
                            label="监听扩展名"
                            value={mount.options?.watch_extensions || ""}
                            onChange={(value) =>
                              update(
                                [
                                  "projects",
                                  projectId,
                                  "types",
                                  typeId,
                                  "mounts",
                                  mountId,
                                  "options",
                                  "watch_extensions",
                                ],
                                value,
                              )
                            }
                          />
                          <Field className="justify-end">
                            <Toggle
                              checked={
                                mount.options?.watch === true ||
                                mount.options?.watch === "true"
                              }
                              label="文件监听"
                              onChange={(value) =>
                                update(
                                  [
                                    "projects",
                                    projectId,
                                    "types",
                                    typeId,
                                    "mounts",
                                    mountId,
                                    "options",
                                    "watch",
                                  ],
                                  value,
                                )
                              }
                            />
                          </Field>
                        </FieldGroup>
                      </div>
                    ),
                  )}
                </div>
              </div>
            ),
          )}
          <Separator />
        </section>
      ))}
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  type = "text",
}: {
  label: string;
  value: string | number;
  onChange: (v: string) => void;
  type?: "text" | "number";
}) {
  const id = useId();
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input
        id={id}
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </Field>
  );
}
function Toggle({
  checked,
  label,
  onChange,
}: {
  checked: boolean;
  label: string;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
      <Switch checked={checked} onCheckedChange={onChange} aria-label={label} />
      {label}
    </label>
  );
}
