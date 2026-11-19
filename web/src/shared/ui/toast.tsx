import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { X } from "lucide-react";
import { cn } from "@/shared/lib/utils";

// Toasts per DESIGN section 9: slide-in from bottom-right, 6s lifetime,
// dismissible, max 3 stacked.

export type ToastTone = "success" | "error" | "info";

export interface Toast {
  id: number;
  tone: ToastTone;
  title: string;
  message?: string;
}

interface ToastContextValue {
  push: (toast: Omit<Toast, "id">) => void;
  dismiss: (id: number) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const TOAST_TONE_STYLE: Record<ToastTone, string> = {
  success: "border-success/40",
  error: "border-destructive/40",
  info: "border-info/40",
};

const TOAST_DOT_STYLE: Record<ToastTone, string> = {
  success: "bg-success",
  error: "bg-destructive",
  info: "bg-info",
};

const MAX_TOASTS = 3;
const TOAST_TTL_MS = 6_000;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(1);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (toast: Omit<Toast, "id">) => {
      const id = nextId.current;
      nextId.current += 1;
      setToasts((prev) => [...prev.slice(-(MAX_TOASTS - 1)), { ...toast, id }]);
      window.setTimeout(() => dismiss(id), TOAST_TTL_MS);
    },
    [dismiss],
  );

  const value = useMemo(() => ({ push, dismiss }), [push, dismiss]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 flex-col gap-2"
      >
        {toasts.map((toast) => (
          <div
            key={toast.id}
            role="status"
            className={cn(
              "toast-enter pointer-events-auto rounded-lg border bg-background p-3 shadow-lg",
              TOAST_TONE_STYLE[toast.tone],
            )}
          >
            <div className="flex items-start gap-2">
              <span
                className={cn("mt-1.5 size-2 shrink-0 rounded-full", TOAST_DOT_STYLE[toast.tone])}
                aria-hidden
              />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium">{toast.title}</p>
                {toast.message !== undefined && (
                  <p className="mt-0.5 break-words text-xs text-gray-500">{toast.message}</p>
                )}
              </div>
              <button
                type="button"
                onClick={() => dismiss(toast.id)}
                className="rounded p-0.5 text-gray-500 hover:bg-surface"
                aria-label="Dismiss notification"
              >
                <X className="size-4" aria-hidden />
              </button>
            </div>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToasts(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (ctx === null) {
    throw new Error("useToasts must be used inside <ToastProvider>");
  }
  return ctx;
}
