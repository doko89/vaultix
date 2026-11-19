import type { ReactNode } from "react";

// Shared page header: h1 24px/semibold per DESIGN section 2 with an
// optional single primary action slot (DESIGN section 3: max one per screen).
export interface PageHeaderProps {
  title: string;
  description: string;
  action?: ReactNode;
}

export function PageHeader({ title, description, action }: PageHeaderProps) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold leading-tight">{title}</h1>
        <p className="mt-1 text-sm text-gray-500">{description}</p>
      </div>
      {action}
    </div>
  );
}
