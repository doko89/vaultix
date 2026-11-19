import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: secret profiles per PRD sections 7.7 and 10.2.
// The form must never render plaintext secrets (DESIGN section 7).
export function CredentialsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Credentials"
        description="Manage protected database and service credential profiles."
        action={
          // TODO(credentials): open the profile editor.
          <Button type="button">
            <Plus aria-hidden />
            Add credential
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Profiles</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The profile list (name, engine, reference, metadata — never plaintext) will appear
            here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
