import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: recurring schedule management per PRD section 7.9.
// Owned by the policies feature (TECH_SPEC section 1.2).
export function SchedulesPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Schedules"
        description="Manage one-time and recurring backup schedules."
        action={
          // TODO(policies): open the schedule editor.
          <Button type="button">
            <Plus aria-hidden />
            Create schedule
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search schedules…"
          aria-label="Search schedules"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All schedules</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The schedule list (policy, cadence, timezone, pause/resume, concurrency limits) will
            appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
