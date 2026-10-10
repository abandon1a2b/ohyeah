import { useCallback, useEffect, useState } from "react";
import {
  Copy,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldSet,
  FieldLegend,
} from "@/components/ui/field";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectGroup,
  SelectItem,
} from "@/components/ui/select";
import {
  Table,
  TableHeader,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
} from "@/components/ui/table";
import { Badge, Empty, Notice } from "../components";
import {
  securityAPI,
  type Credential,
  type Principal,
  type PrincipalPolicy,
  type AuditEvent,
} from "../security-api";
import type { Identity } from "../auth";
import type { Project, Source } from "../types";

const blank = (): Principal => ({
  subject: "",
  name: "",
  admin: false,
  disabled: false,
  projects: [],
  syncProjects: [],
  syncSources: [],
  uploadSources: [],
});
const date = (seconds: number) =>
  seconds ? new Date(seconds * 1000).toLocaleString() : "-";

export function AccessPage({
  identity,
  projects,
  sources,
}: {
  identity: Identity;
  projects: Project[];
  sources: Source[];
}) {
  const [tab, setTab] = useState("tokens");
  const [all, setAll] = useState(identity.admin);
  const [tokens, setTokens] = useState<Credential[]>([]);
  const [policies, setPolicies] = useState<PrincipalPolicy[]>([]);
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [secret, setSecret] = useState("");
  const [editing, setEditing] = useState<{
    principal: Principal;
    version: number;
    isNew: boolean;
  } | null>(null);
  const [issuing, setIssuing] = useState(false);
  const [subject, setSubject] = useState("");
  const [name, setName] = useState("");
  const maxHours = Math.max(1 / 60, (identity.tokenTTLSeconds ?? 86400) / 3600);
  const [hours, setHours] = useState(Math.min(24, maxHours));

  const load = useCallback(async () => {
    if (tab === "audit") {
      const data = await securityAPI.audit(all);
      setEvents(data);
      setMore(data.length === 50);
    } else if (tab === "principals")
      setPolicies(await securityAPI.principals());
    else setTokens(await securityAPI.tokens(all));
  }, [tab, all]);
  useEffect(() => {
    let active = true;
    setBusy(true);
    load()
      .catch((e) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setBusy(false);
      });
    return () => {
      active = false;
    };
  }, [load]);
  const run = async (action: () => Promise<unknown>, reload = true) => {
    setBusy(true);
    setError("");
    try {
      await action();
      if (reload) await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const beginIssue = () =>
    run(async () => {
      const list = await securityAPI.principals();
      setPolicies(list);
      setSubject(list.find((p) => !p.principal?.disabled)?.subject ?? "");
      setName("");
      setHours(Math.min(24, maxHours));
      setIssuing(true);
    });
  const save = () =>
    run(async () => {
      if (!editing) return;
      let version = editing.version;
      if (editing.isNew) {
        const existing = await securityAPI.principal(editing.principal.subject);
        if (existing.principal) throw new Error("账号已存在，请从列表编辑");
        version = existing.version;
      }
      await securityAPI.save(editing.principal, version);
      setEditing(null);
    });
  const update = (patch: Partial<Principal>) =>
    setEditing((current) =>
      current
        ? { ...current, principal: { ...current.principal, ...patch } }
        : null,
    );
  const grants = (
    key: "projects" | "syncProjects" | "syncSources" | "uploadSources",
    label: string,
    values: string[],
  ) => (
    <FieldSet>
      <FieldLegend>{label}</FieldLegend>
      <div className="flex flex-col gap-2">
        {values.length ? (
          values.map((value) => (
            <label key={value} className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                className="mt-0.5 size-4 shrink-0"
                checked={(editing?.principal[key] ?? []).includes(value)}
                onChange={(e) =>
                  update({
                    [key]: e.target.checked
                      ? [...(editing?.principal[key] ?? []), value]
                      : (editing?.principal[key] ?? []).filter(
                          (v) => v !== value,
                        ),
                  })
                }
              />
              <span className="break-all">{value}</span>
            </label>
          ))
        ) : (
          <span className="text-sm text-muted-foreground">无可用来源</span>
        )}
      </div>
    </FieldSet>
  );

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">访问管理</h2>
        <div className="flex gap-2">
          <Button
            size="icon-sm"
            variant="outline"
            title="刷新"
            aria-label="刷新访问管理"
            disabled={busy}
            onClick={() => run(async () => {})}
          >
            <RefreshCw />
          </Button>
          {identity.admin && tab === "tokens" && (
            <Button disabled={busy} onClick={beginIssue}>
              <KeyRound />
              签发令牌
            </Button>
          )}
          {identity.admin && tab === "principals" && (
            <Button
              disabled={busy}
              onClick={() =>
                setEditing({ principal: blank(), version: 0, isNew: true })
              }
            >
              <Plus />
              添加账号
            </Button>
          )}
        </div>
      </div>
      {error && (
        <Notice kind="error" close={() => setError("")}>
          {error}
        </Notice>
      )}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b pb-3">
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="tokens">凭据</TabsTrigger>
            {identity.admin && (
              <TabsTrigger value="principals">账号权限</TabsTrigger>
            )}
            <TabsTrigger value="audit">审计</TabsTrigger>
          </TabsList>
        </Tabs>
        {identity.admin && tab !== "principals" && (
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={all}
              onChange={(e) => setAll(e.target.checked)}
            />
            全部账号
          </label>
        )}
      </div>
      {tab === "tokens" &&
        (tokens.length ? (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>账号 / 名称</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>到期</TableHead>
                  <TableHead>最近使用</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((token) => (
                  <TableRow key={token.id}>
                    <TableCell>
                      <div className="font-medium">{token.subject}</div>
                      <div className="text-xs text-muted-foreground">
                        {token.name || token.id.slice(0, 12)}
                        {token.id === identity.credential?.id
                          ? " · 当前会话"
                          : ""}
                      </div>
                    </TableCell>
                    <TableCell>
                      {token.kind === "session" ? "浏览器" : "API"}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      {date(token.expiresAt)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      {date(token.lastUsedAt)}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1">
                        {token.kind === "token" && (
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            title="轮换令牌"
                            aria-label={`轮换 ${token.name || token.id}`}
                            disabled={busy}
                            onClick={() =>
                              run(async () =>
                                setSecret(
                                  (await securityAPI.rotate(token.id)).token,
                                ),
                              )
                            }
                          >
                            <RotateCcw />
                          </Button>
                        )}
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          title="撤销凭据"
                          aria-label={`撤销 ${token.name || token.id}`}
                          disabled={busy}
                          onClick={() =>
                            run(() => securityAPI.revoke(token.id))
                          }
                        >
                          <Trash2 />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <Empty title={busy ? "加载中" : "没有有效凭据"} />
        ))}
      {tab === "principals" && (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>账号</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>权限</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {policies.map((policy) => {
                const p = policy.principal;
                if (!p) return null;
                return (
                  <TableRow key={policy.subject}>
                    <TableCell>
                      <div className="font-medium">{p.name || p.subject}</div>
                      <div
                        className="max-w-64 truncate text-xs text-muted-foreground"
                        title={p.subject}
                      >
                        {p.subject}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge tone={p.disabled ? "neutral" : "success"}>
                        {p.disabled ? "禁用" : "启用"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {p.admin
                        ? "管理员"
                        : `读取 ${(p.projects ?? []).length} 个项目 · 写入 ${(p.uploadSources ?? []).length} 个来源`}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1">
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          title="编辑权限"
                          aria-label={`编辑 ${p.subject}`}
                          disabled={busy}
                          onClick={() =>
                            setEditing({
                              principal: structuredClone(p),
                              version: policy.version,
                              isNew: false,
                            })
                          }
                        >
                          <Pencil />
                        </Button>
                        {policy.version > 0 && (
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            title="恢复启动配置"
                            aria-label={`恢复 ${p.subject}`}
                            disabled={busy}
                            onClick={() =>
                              run(() =>
                                securityAPI.reset(p.subject, policy.version),
                              )
                            }
                          >
                            <RotateCcw />
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
      {tab === "audit" && (
        <>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>账号</TableHead>
                  <TableHead>操作</TableHead>
                  <TableHead>资源</TableHead>
                  <TableHead>结果</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map((event) => (
                  <TableRow key={event.id}>
                    <TableCell className="whitespace-nowrap">
                      {date(event.occurredAt)}
                    </TableCell>
                    <TableCell>{event.subject}</TableCell>
                    <TableCell>{event.action}</TableCell>
                    <TableCell
                      className="max-w-80 truncate"
                      title={event.resource}
                    >
                      {event.resource}
                    </TableCell>
                    <TableCell>
                      <Badge
                        tone={
                          event.status >= 400
                            ? "warning"
                            : event.status === 0
                              ? "neutral"
                              : "success"
                        }
                      >
                        {event.status || "未完成"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {!events.length && <Empty title={busy ? "加载中" : "暂无审计记录"} />}
          {more && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() =>
                run(async () => {
                  const data = await securityAPI.audit(all, events.at(-1)?.id);
                  setEvents([...events, ...data]);
                  setMore(data.length === 50);
                }, false)
              }
            >
              更早记录
            </Button>
          )}
        </>
      )}
      <Sheet
        open={!!editing}
        onOpenChange={(open) => {
          if (!open) setEditing(null);
        }}
      >
        <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{editing?.isNew ? "添加账号" : "账号权限"}</SheetTitle>
            <SheetDescription>
              {editing?.isNew
                ? "OIDC subject / 服务账号标识"
                : editing?.principal.subject}
            </SheetDescription>
          </SheetHeader>
          {editing && (
            <form
              className="flex flex-col gap-6 px-4 pb-6"
              onSubmit={(e) => {
                e.preventDefault();
                void save();
              }}
            >
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="principal-subject">账号标识</FieldLabel>
                  <Input
                    id="principal-subject"
                    required
                    maxLength={256}
                    value={editing.principal.subject}
                    disabled={!editing.isNew}
                    onChange={(e) => update({ subject: e.target.value })}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="principal-name">名称</FieldLabel>
                  <Input
                    id="principal-name"
                    maxLength={100}
                    value={editing.principal.name}
                    onChange={(e) => update({ name: e.target.value })}
                  />
                </Field>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={!editing.principal.disabled}
                    onChange={(e) => update({ disabled: !e.target.checked })}
                  />
                  启用
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={editing.principal.admin}
                    onChange={(e) => update({ admin: e.target.checked })}
                  />
                  管理员
                </label>
                {grants(
                  "projects",
                  "项目读取",
                  projects.map((p) => p.id),
                )}
                {grants(
                  "syncProjects",
                  "项目同步",
                  projects.map((p) => p.id),
                )}
                {grants(
                  "syncSources",
                  "来源同步",
                  sources.filter((s) => s.driver !== "upload").map((s) => s.id),
                )}
                {grants(
                  "uploadSources",
                  "来源上传",
                  sources.filter((s) => s.driver === "upload").map((s) => s.id),
                )}
              </FieldGroup>
              {error && (
                <p
                  role="alert"
                  className="break-words text-sm text-destructive"
                >
                  {error}
                </p>
              )}
              <Button type="submit" disabled={busy}>
                保存权限
              </Button>
            </form>
          )}
        </SheetContent>
      </Sheet>
      <Sheet open={issuing} onOpenChange={setIssuing}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>签发访问令牌</SheetTitle>
            <SheetDescription>服务账号或已授权用户</SheetDescription>
          </SheetHeader>
          <form
            className="flex flex-col gap-6 px-4"
            onSubmit={(e) => {
              e.preventDefault();
              void run(async () => {
                const result = await securityAPI.issue(
                  subject,
                  name,
                  Math.floor(hours * 3600),
                );
                setIssuing(false);
                setSecret(result.token);
              });
            }}
          >
            <FieldGroup>
              <Field>
                <FieldLabel>账号</FieldLabel>
                <Select value={subject} onValueChange={setSubject}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {policies
                        .filter((p) => p.principal && !p.principal.disabled)
                        .map((p) => (
                          <SelectItem key={p.subject} value={p.subject}>
                            {p.subject}
                          </SelectItem>
                        ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="token-name">名称</FieldLabel>
                <Input
                  id="token-name"
                  required
                  maxLength={100}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="token-hours">有效期（小时）</FieldLabel>
                <Input
                  id="token-hours"
                  type="number"
                  required
                  min={1 / 60}
                  max={maxHours}
                  step="any"
                  value={hours}
                  onChange={(e) => setHours(Number(e.target.value))}
                />
              </Field>
            </FieldGroup>
            {error && (
              <p role="alert" className="break-words text-sm text-destructive">
                {error}
              </p>
            )}
            <Button type="submit" disabled={busy || !subject}>
              签发
            </Button>
          </form>
        </SheetContent>
      </Sheet>
      <Sheet
        open={!!secret}
        onOpenChange={(open) => {
          if (!open) setSecret("");
        }}
      >
        <SheetContent>
          <SheetHeader>
            <SheetTitle>访问令牌</SheetTitle>
            <SheetDescription>仅显示一次</SheetDescription>
          </SheetHeader>
          <div className="flex flex-col gap-4 px-4">
            <Textarea
              aria-label="新访问令牌"
              readOnly
              value={secret}
              className="break-all"
            />
            <Button
              onClick={() =>
                navigator.clipboard
                  .writeText(secret)
                  .catch((e) => setError(e.message))
              }
            >
              <Copy />
              复制令牌
            </Button>
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}
