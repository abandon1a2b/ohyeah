import type { SyncRun } from "./types";

const possiblyInterruptedAfterMs = 60 * 60 * 1000;

export function finishLabel(run: SyncRun, now = Date.now()) {
  if (run.finishedAt) return new Date(run.finishedAt).toLocaleString();
  if (run.status !== "running") return "未记录";
  const started = new Date(run.startedAt).getTime();
  return now - started > possiblyInterruptedAfterMs
    ? "未结束（可能中断）"
    : "进行中";
}

export function durationLabel(run: SyncRun) {
  if (!run.finishedAt) return "—";
  const milliseconds = new Date(run.finishedAt).getTime() - new Date(run.startedAt).getTime();
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return "—";
  const seconds = Math.floor(milliseconds / 1000);
  if (seconds < 60) return `${(milliseconds / 1000).toFixed(1)} 秒`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分 ${seconds % 60} 秒`;
  return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`;
}
