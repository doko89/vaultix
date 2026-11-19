import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: snapshot catalog per PRD section 7.13. Browse-only view,
// so no primary action (DESIGN section 3 allows zero or one).
export function CatalogPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Catalog"
        description="Browse snapshots and manifests by agent, policy, target, and date."
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search snapshots…"
          aria-label="Search snapshots"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All snapshots</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The snapshot list (manifest metadata, encryption label, restorable state) will
            appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
