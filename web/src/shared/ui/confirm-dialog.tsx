import { useEffect, useState } from "react";
import { Button } from "@/shared/ui/button";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";

// Destructive confirm per DESIGN sections 3 and 7: explicit confirmation,
// optionally requiring the target name to be typed. The confirm button never
// moves while the dialog is open.

export interface ConfirmDialogProps {
  open: boolean;
  title: string;
  body: string;
  confirmLabel: string;
  /** When set, the user must type this exact name to confirm. */
  requireName?: string | undefined;
  busy?: boolean | undefined;
  onConfirm: () => void;
  onClose: () => void;
}

export function ConfirmDialog({
  open,
  title,
  body,
  confirmLabel,
  requireName,
  busy,
  onConfirm,
  onClose,
}: ConfirmDialogProps) {
  const [typed, setTyped] = useState("");

  useEffect(() => {
    if (open) setTyped("");
  }, [open ]);

  const blocked = requireName !== undefined && typed !== requireName;

  return (
    <Dialog open={open} title={title} onClose={onClose}>
      <p className="text-sm text-gray-500">{body}</p>
      {requireName !== undefined && (
        <div className="mt-4">
          <label htmlFor="confirm-name" className="mb-1 block text-sm font-medium">
            Type <span className="font-mono">{requireName}</span> to confirm
          </label>
          <Input
            id="confirm-name"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            placeholder={requireName}
          />
        </div>
      )}
      <div className="mt-6 flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose} disabled={busy === true}>
          Cancel
        </Button>
        <Button
          type="button"
          variant="destructive"
          onClick={onConfirm}
          disabled={blocked || busy === true}
        >
          {busy === true ? "Working…" : confirmLabel}
        </Button>
      </div>
    </Dialog>
  );
}
