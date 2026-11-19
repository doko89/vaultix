import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: S3 destination config + connection test per PRD section 7.12.
export function StoragePage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Storage"
        description="Configure and test S3-compatible object storage destinations."
        action={
          // TODO(storage): open the destination editor.
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
            The destination list (endpoint, region, bucket, prefix, test-connection button)
            will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
