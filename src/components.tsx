import type { ReactNode } from "react";
import { AlertTriangle, CheckCircle2, LoaderCircle, X } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge as UiBadge } from "@/components/ui/badge";
import { Button as UiButton } from "@/components/ui/button";
import {
  Empty as UiEmpty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { Label } from "@/components/ui/label";

export function Button({
  children,
  onClick,
  variant = "primary",
  disabled = false,
  title,
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  title?: string;
}) {
  return (
    <UiButton
      title={title}
      disabled={disabled}
      onClick={onClick}
      variant={
        variant === "secondary"
          ? "outline"
          : variant === "danger"
            ? "destructive"
            : "default"
      }
    >
      {children}
    </UiButton>
  );
}

export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: "neutral" | "success" | "warning" | "danger" | "info";
}) {
  const variant =
    tone === "danger"
      ? "destructive"
      : tone === "neutral"
        ? "secondary"
        : "outline";
  return (
    <UiBadge variant={variant} data-tone={tone} className="shrink-0">
      {children}
    </UiBadge>
  );
}

export function Empty({ title, detail }: { title: string; detail?: string }) {
  return (
    <UiEmpty className="min-h-44 rounded-none border-y">
      <EmptyHeader>
        <EmptyTitle>{title}</EmptyTitle>
        {detail && <EmptyDescription>{detail}</EmptyDescription>}
      </EmptyHeader>
    </UiEmpty>
  );
}

export function Spinner({ label = "处理中" }: { label?: string }) {
  return (
    <span className="inline-flex items-center gap-2">
      <LoaderCircle data-icon="inline-start" className="animate-spin" />
      {label}
    </span>
  );
}

export function Notice({
  kind = "success",
  children,
  close,
}: {
  kind?: "success" | "error";
  children: ReactNode;
  close?: () => void;
}) {
  const Icon = kind === "success" ? CheckCircle2 : AlertTriangle;
  return (
    <Alert
      variant={kind === "error" ? "destructive" : "default"}
      className="flex items-center gap-3"
    >
      <Icon className="size-4 shrink-0" />
      <AlertDescription className="flex-1">{children}</AlertDescription>
      {close && (
        <UiButton
          variant="ghost"
          size="icon-sm"
          title="关闭提示"
          onClick={close}
        >
          <X />
        </UiButton>
      )}
    </Alert>
  );
}

export function FieldLabel({ children }: { children: ReactNode }) {
  return <Label className="mb-2 text-muted-foreground">{children}</Label>;
}
