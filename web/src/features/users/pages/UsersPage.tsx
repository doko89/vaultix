import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";

// Placeholder: local users, RBAC roles, OIDC links per PRD sections 5, 7.1.1.
export function UsersPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Users & Roles"
        description="Manage access, permissions, OIDC links, and group-to-role mapping."
        action={
          // TODO(users): open the user editor.
          <Button type="button">
            <Plus aria-hidden />
            Add user
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search users…"
          aria-label="Search users"
          className="max-w-sm"
        />
        <Button type="button" variant="outline">
          Filter
        </Button>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All users</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-gray-500">
            The user list (roles, OIDC identity links, group mapping) will appear here.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
