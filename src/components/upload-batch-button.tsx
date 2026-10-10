import { useRef, useState } from "react";
import { Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import { api } from "@/api";

type Batch = {
  sourceId: string;
  batchId: string;
  envelopes: { type: string; mode?: string }[];
};
export function UploadBatchButton({
  sourceId,
  disabled,
  onUploaded,
}: {
  sourceId: string;
  disabled: boolean;
  onUploaded: (pending: boolean) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [batch, setBatch] = useState<Batch | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function selected(file?: File) {
    setError("");
    if (!file) return;
    try {
      if (file.size > 32 * 1024 * 1024) throw new Error("批次不能超过 32 MiB");
      const value = JSON.parse(await file.text()) as Batch;
      if (
        value.sourceId !== sourceId ||
        !value.batchId ||
        !Array.isArray(value.envelopes)
      )
        throw new Error("批次来源或格式不匹配");
      setBatch(value);
    } catch (e) {
      setError((e as Error).message);
    }
    if (input.current) input.current.value = "";
  }
  async function submit() {
    setBusy(true);
    setError("");
    try {
      const result = await api.upload(batch);
      setBatch(null);
      onUploaded(result.indexPending);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <input
        ref={input}
        type="file"
        hidden
        accept=".json,application/json"
        aria-label={`上传批次 ${sourceId}`}
        onChange={(e) => void selected(e.target.files?.[0])}
      />
      <Button
        size="sm"
        variant="outline"
        disabled={disabled || busy}
        onClick={() => input.current?.click()}
      >
        <Upload />
        上传
      </Button>
      {error && !batch && (
        <p
          role="alert"
          className="max-w-64 break-words text-xs text-destructive"
        >
          {error}
        </p>
      )}
      <Sheet
        open={!!batch}
        onOpenChange={(open) => {
          if (!open) {
            setBatch(null);
            setError("");
          }
        }}
      >
        <SheetContent>
          <SheetHeader>
            <SheetTitle>提交上传批次</SheetTitle>
            <SheetDescription className="break-all">
              {sourceId}
            </SheetDescription>
          </SheetHeader>
          <div className="flex flex-col gap-6 px-4">
            <dl className="grid gap-3 text-sm">
              <div>
                <dt className="text-muted-foreground">批次 ID</dt>
                <dd className="break-all">{batch?.batchId}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">模式</dt>
                <dd>
                  {batch?.envelopes.find((e) => e.type === "complete")?.mode}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">文档</dt>
                <dd>
                  {batch?.envelopes.filter((e) => e.type === "document").length}
                </dd>
              </div>
            </dl>
            {error && (
              <p role="alert" className="break-words text-sm text-destructive">
                {error}
              </p>
            )}
            <Button disabled={busy} onClick={() => void submit()}>
              <Upload />
              {busy ? "提交中" : "提交上传"}
            </Button>
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
