import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: alert destinations per PRD section 7.16.
export function NotificationsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Notifications"
        description="Configure email and webhook destinations for failed jobs and agent changes."
        action={
          // TODO(notifications): open the destination editor.
          <Button type="button">
            <Plus aria-hidden />
            Add destination
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Destinations</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The destination list (channel, target, enabled state) will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
