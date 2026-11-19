import { cn } from "@/shared/lib/utils";

// Determinate progress bar per DESIGN section 9 (jobs with measurable
// bytes). Spinners are reserved for unmeasurable ops (connection tests).

export function Progress({ value, className }: { value: number | undefined | null; className?: string }) {
  const pct = value === undefined || value === null ? undefined : Math.max(0, Math.min(1, value));
  return (
    <div
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct === undefined ? undefined : Math.round(pct * 100)}
      className={cn("h-2 w-full overflow-hidden rounded-full bg-surface", className)}
    >
      {pct !== undefined && <div className="h-full rounded-full bg-primary" style={{ width: `${pct * 100}%` }} />}
    </div>
  );
}
