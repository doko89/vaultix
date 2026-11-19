import { RotateCcw } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: restore wizard + restore job status per PRD section 7.13.
export function RestorePage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Restore"
        description="Create and monitor restore jobs with explicit confirmation."
        action={
          // TODO(restore): open the restore wizard.
          <Button type="button">
            <RotateCcw aria-hidden />
            Start restore
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Restore jobs</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The restore wizard (file, database, volume, stack) and restore job status will
            appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
