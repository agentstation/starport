import { toast } from "sonner";

// The console announces each write's outcome in one place, so every page
// says "Key created" the same way, a screen reader hears it, and no component
// keeps its own notice state and timer. DESIGN.md: toasts sit bottom-right,
// hold one line, and dismiss themselves.

// errorText reads the reason out of a failure, whatever threw it.
export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// announce names an outcome that happened: "Key created", "Team deleted".
export function announce(text: string): void {
  toast.success(text);
}

// report names what failed and why. The text states the next action when
// the operator has one.
export function report(text: string): void {
  toast.error(text);
}

// settled builds the onSettled callback for a write whose failure needs no
// inline slot: success announces the outcome, and failure reports what
// failed with the gateway's reason.
export function settled(success: string, failure: string) {
  return (_result: unknown, error: unknown) => {
    if (error) report(`${failure}: ${errorText(error)}`);
    else announce(success);
  };
}
