import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import { X } from "lucide-react";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/button";

// Lightweight accessible dialog: backdrop + Escape to close + initial focus.
// Keeps bundle small (no extra deps) while covering every feature's modals.

export interface DialogProps {
  open: boolean;
  title: string;
  description?: string | undefined;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean | undefined;
}

export function Dialog({ open, title, description, onClose, children, wide }: DialogProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);

  useEffect(() => {
    onCloseRef.current = onClose;
  });

  // Runs on open only: focusing here must not depend on onClose identity,
  // or every parent re-render (e.g. each keystroke in a page-level input)
  // would yank focus back to the panel. Escape always calls the latest.
  useEffect(() => {
    if (!open) return;
    panelRef.current?.focus();
    function onKey(event: KeyboardEvent): void {
      if (event.key === "Escape") onCloseRef.current();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-40" role="dialog" aria-modal="true" aria-label={title}>
      <div className="absolute inset-0 bg-black/40" onClick={onClose} aria-hidden />
      <div className="absolute inset-0 overflow-y-auto p-4">
        <div
          ref={panelRef}
          tabIndex={-1}
          className={cn(
            "dialog-enter mx-auto my-8 rounded-lg border border-border bg-background shadow-xl outline-none",
            wide === true ? "max-w-2xl" : "max-w-lg",
          )}
        >
          <div className="flex items-start justify-between gap-4 border-b border-border p-4">
            <div>
              <h2 className="text-lg font-semibold leading-none">{title}</h2>
              {description !== undefined && (
                <p className="mt-1 text-sm text-gray-500">{description}</p>
              )}
            </div>
            <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close dialog">
              <X className="size-4" aria-hidden />
            </Button>
          </div>
          <div className="p-4">{children}</div>
        </div>
      </div>
    </div>
  );
}
