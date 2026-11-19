import { TriangleAlert } from "lucide-react";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/button";

// Inline banner for actionable errors (e.g. OIDC callback failures on
// /login, DESIGN section 10). Never render tokens or secrets here.
export interface AlertProps {
  tone: "error" | "info";
  message: string;
  actionLabel?: string;
  onAction?: () => void;
}

export function Alert({ tone, message, actionLabel, onAction }: AlertProps) {
  return (
    <div
      role="alert"
      className={cn(
        "flex items-start gap-2 rounded-md border p-3 text-sm",
        tone === "error"
          ? "border-destructive/30 bg-destructive/10 text-destructive"
          : "border-info/30 bg-info/10 text-info",
      )}
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="flex-1">
        <p>{message}</p>
        {actionLabel !== undefined && onAction !== undefined && (
          <Button variant="link" size="sm" className="h-auto p-0" onClick={onAction}>
            {actionLabel}
          </Button>
        )}
      </div>
    </div>
  );
}
