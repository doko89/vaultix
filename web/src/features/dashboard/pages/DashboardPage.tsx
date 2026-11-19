import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: stat cards + health overview per PRD section 7.15.
// Wired to useDashboardSummary once the Manager API lands.
const STATS = [
  { label: "Total agents", value: "—" },
  { label: "Online", value: "—" },
  { label: "Offline", value: "—" },
  { label: "Failed jobs (24h)", value: "—" },
] as const;

export function DashboardPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Dashboard"
        description="System overview and backup health across all agents."
      />
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {STATS.map((stat) => (
          <Card key={stat.label}>
            <CardHeader>
              <CardTitle>{stat.label}</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-2xl font-semibold">{stat.value}</p>
            </CardContent>
          </Card>
        ))}
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Backup trends</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            Duration and volume trends, latest backup per agent/policy, and policies without a
            recent successful backup will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
