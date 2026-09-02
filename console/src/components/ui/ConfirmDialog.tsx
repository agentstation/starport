import type { ReactNode } from "react";

import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogError,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { DestructiveButton, GhostButton } from "@/components/ui/Form";

// ConfirmDialog is the one shape for a write the operator cannot take back.
// The title names the verb, the body restates the object and what the write
// reaches, and the footer keeps Cancel first. A refusal keeps the dialog
// open, so the gateway's reason lands in the error slot next to the control
// that drew it instead of in a toast that fades.
export function ConfirmDialog({
  title,
  action,
  dismiss = "Cancel",
  error,
  pending,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  action: string;
  dismiss?: string;
  error?: string;
  pending?: boolean;
  onConfirm: () => void;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="flex flex-col gap-2 text-sm text-text-2">
            {children}
            <p className="text-xs text-text-3">There is no undo.</p>
          </div>
        </DialogBody>
        <DialogError>{error}</DialogError>
        <DialogFooter>
          <GhostButton onClick={onClose}>{dismiss}</GhostButton>
          <DestructiveButton onClick={onConfirm} disabled={pending}>
            {action}
          </DestructiveButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// reasonOf reads the message a failed write carries, for the error slot.
export function reasonOf(problem: unknown): string {
  return problem instanceof Error ? problem.message : String(problem);
}
