import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: audit trail per PRD sections 7.16 and 10.2.
// Read-only view, so no primary action.
export function AuditLogsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Audit Logs"
        description="Review administrative and security events."
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search by actor, action, or target…"
          aria-label="Search audit logs"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Events</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The audit table (actor, action, target, timestamp, outcome) will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
