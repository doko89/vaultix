import type { HTMLAttributes } from "react";
import { cn } from "@/shared/lib/utils";
import type { AgentPresence, JobState } from "@/shared/types";

// DESIGN section 6: job + agent state badges. All state badges in every
// feature must use these components so colors stay on the DESIGN tokens.

const JOB_STATE_LABEL: Record<JobState, string> = {
  queued: "Queued",
  assigned: "Assigned",
  running: "Running",
  uploading: "Uploading",
  verifying: "Verifying",
  succeeded: "Succeeded",
  failed: "Failed",
  cancelled: "Cancelled",
};

const JOB_STATE_STYLE: Record<JobState, string> = {
  queued: "border-info/30 bg-info/10 text-info",
  assigned: "border-info/30 bg-info/10 text-info",
  running: "border-warning/30 bg-warning/10 text-warning",
  uploading: "border-warning/30 bg-warning/10 text-warning",
  verifying: "border-warning/30 bg-warning/10 text-warning",
  succeeded: "border-success/30 bg-success/10 text-success",
  failed: "border-destructive/30 bg-destructive/10 text-destructive",
  cancelled: "border-border bg-surface text-gray-500",
};

const PULSING_STATES: ReadonlySet<JobState> = new Set(["running", "uploading"]);

export interface StateBadgeProps extends HTMLAttributes<HTMLSpanElement> {
  state: JobState;
}

export function StateBadge({ state, className, ...rest }: StateBadgeProps) {
  return (
    <span
      {...rest}
      className={cn(
        "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium",
        JOB_STATE_STYLE[state],
        PULSING_STATES.has(state) && "animate-pulse motion-reduce:animate-none",
        className,
      )}
    >
      {JOB_STATE_LABEL[state]}
    </span>
  );
}

const PRESENCE_LABEL: Record<AgentPresence, string> = {
  online: "Online",
  offline: "Offline",
  stale: "Stale",
};

const PRESENCE_STYLE: Record<AgentPresence, string> = {
  online: "border-success/30 bg-success/10 text-success",
  offline: "border-destructive/30 bg-destructive/10 text-destructive",
  stale: "border-warning/30 bg-warning/10 text-warning",
};

export interface PresenceBadgeProps extends HTMLAttributes<HTMLSpanElement> {
  presence: AgentPresence;
}

export function PresenceBadge({ presence, className, ...rest }: PresenceBadgeProps) {
  return (
    <span
      {...rest}
      className={cn(
        "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium",
        PRESENCE_STYLE[presence],
        className,
      )}
    >
      {PRESENCE_LABEL[presence]}
    </span>
  );
}
