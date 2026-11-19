import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: policy CRUD per PRD sections 7.4-7.8 and 7.11.
export function PoliciesPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Policies"
        description="Create, edit, assign, and disable backup policies."
        action={
          // TODO(policies): open the policy editor.
          <Button type="button">
            <Plus aria-hidden />
            Create policy
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search policies…"
          aria-label="Search policies"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All policies</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The policy list (name, target agents, target type, schedule, retention, destination,
            enabled state) will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
