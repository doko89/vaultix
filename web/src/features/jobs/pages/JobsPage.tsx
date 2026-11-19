import { Play } from "lucide-react";
import type { JobState } from "@/shared/types";
import { StateBadge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

const ALL_STATES: JobState[] = [
  "queued",
  "assigned",
  "running",
  "uploading",
  "verifying",
  "succeeded",
  "failed",
  "cancelled",
];

// Placeholder: job table + detail per PRD section 7.10.
export function JobsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Jobs"
        description="Monitor, inspect, retry, and cancel backup jobs."
        action={
          // TODO(jobs): open manual-run dialog.
          <Button type="button">
            <Play aria-hidden />
            Run manual backup
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search by job, agent, or policy…"
          aria-label="Search jobs"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All jobs</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-2">
            {ALL_STATES.map((state) => (
              <StateBadge key={state} state={state} />
            ))}
          </div>
          <p className="mt-4 text-sm text-gray-500">
            The job table (state, progress, bytes, encryption, destination, snapshot, errors)
            will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
