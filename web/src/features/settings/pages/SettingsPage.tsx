import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: global config, OIDC providers, component health
// per PRD sections 7.1.1 and 10.5.
export function SettingsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Settings"
        description="Global configuration, OIDC providers, and system health."
        action={
          // TODO(settings): persist the settings form.
          <Button type="button">Save changes</Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>General</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            Global settings, OIDC provider discovery/test/enable, and component health will
            appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
