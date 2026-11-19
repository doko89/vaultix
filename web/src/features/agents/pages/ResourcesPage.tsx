import { useParams } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: per-agent resource discovery per PRD section 7.3.
export function ResourcesPage() {
  const { agentId } = useParams();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Resource discovery"
        description={`Host paths, containers, volumes, and databases reported by agent ${agentId ?? "unknown"}.`}
      />
      <Card>
        <CardHeader>
          <CardTitle>Discovered resources</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            Discovered host and Docker resources will appear here, grouped by target type
            (DESIGN section 5).
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
