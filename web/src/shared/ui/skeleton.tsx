import { cn } from "@/shared/lib/utils";

// Skeleton loading per DESIGN section 9: shimmer rows for lists, updated
// in place by polling without scroll jumps. Honors reduced-motion via the
// global CSS rule plus motion-reduce here.

export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      className={cn("skeleton-shimmer rounded-md bg-surface", className ?? "h-4 w-full")}
    />
  );
}

export function TableSkeleton({ rows = 5, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <div className="flex flex-col gap-2" aria-label="Loading">
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="flex gap-2">
          {Array.from({ length: cols }, (_, c) => (
            <Skeleton key={c} className="h-9 flex-1" />
          ))}
        </div>
      ))}
    </div>
  );
}

export function CardSkeleton() {
  return (
    <div className="flex flex-col gap-2" aria-label="Loading">
      <Skeleton className="h-5 w-1/3" />
      <Skeleton className="h-4 w-full" />
      <Skeleton className="h-4 w-2/3" />
    </div>
  );
}
