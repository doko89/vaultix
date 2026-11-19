import { Plus } from "lucide-react";
import { PresenceBadge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: agent registry table per PRD section 7.2.
export function AgentsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Agents"
        description="Register, inspect, disable, and monitor agents."
        action={
          // TODO(agents): open the enrollment-token dialog.
          <Button type="button">
            <Plus aria-hidden />
            Enroll agent
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search by hostname or IP…"
          aria-label="Search agents"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All agents</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-2">
            <PresenceBadge presence="online" />
            <PresenceBadge presence="offline" />
            <PresenceBadge presence="stale" />
          </div>
          <p className="mt-4 text-sm text-gray-500">
            The agent table (hostname, IP, OS/arch, version, heartbeat, disk capacity) will
            appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
