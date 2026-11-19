// Shared formatting helpers: byte sizes, timestamps, durations.

const UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

export function formatBytes(bytes: number | bigint | undefined | null): string {
  if (bytes === undefined || bytes === null) return "—";
  const n = typeof bytes === "bigint" ? Number(bytes) : bytes;
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  let value = n;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${UNITS[unit] ?? "B"}`;
}

export function formatDateTime(iso: string | undefined | null): string {
  if (iso === undefined || iso === null || iso === "") return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export function formatDate(iso: string | undefined | null): string {
  if (iso === undefined || iso === null || iso === "") return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString();
}

/** "3m ago", "2h ago", ... for heartbeats and recency columns. */
export function timeAgo(iso: string | undefined | null, nowMs?: number): string {
  if (iso === undefined || iso === null || iso === "") return "never";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";
  const diffMs = Math.max(0, (nowMs ?? Date.now()) - then);
  const minutes = Math.floor(diffMs / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return formatDate(iso);
}

export function formatPercent(ratio: number | undefined | null, digits = 0): string {
  if (ratio === undefined || ratio === null || !Number.isFinite(ratio)) return "—";
  return `${(ratio * 100).toFixed(digits)}%`;
}
