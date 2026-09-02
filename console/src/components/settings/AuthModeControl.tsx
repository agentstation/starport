import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Lock, LockOpen } from "lucide-react";
import { useState, useSyncExternalStore } from "react";

import { GhostButton, PrimaryButton } from "@/components/ui/Form";
import { Dialog, DialogBody, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  ApiError,
  getApiKey,
  onCredentialChange,
  setAuthMode,
  type AuthMode,
} from "@/lib/api";
import { queries } from "@/lib/queries";

const CHOICES: {
  mode: AuthMode["mode"];
  label: string;
  icon: typeof Lock;
}[] = [
  { mode: "required", label: "Require a key", icon: Lock },
  { mode: "disabled", label: "Open", icon: LockOpen },
];

// consequence states what changes the moment the switch commits, in the
// reader's terms. Locking a gateway can lock this browser out of it, and a
// reader who learns that from the next screen learns it too late.
function consequence(next: AuthMode["mode"], hasKey: boolean): string {
  if (next === "required") {
    return hasKey
      ? "Every request will need a gateway API key. This browser has one saved, so the console keeps working; anything else calling this gateway without a key starts getting 401."
      : "Every request will need a gateway API key, and this browser has none saved. The console locks until you save a key under Connection above. Create one on the Keys screen first, or run starport init in a terminal.";
  }
  return "Anyone who can reach this gateway can use it without a key, and their usage is metered against the default account. The admin plane stays closed, and the switch itself can only be changed from this machine.";
}

// AuthModeControl is the gateway-wide authentication switch. It is not a
// console preference: what it changes, every client of this deployment sees.
export function AuthModeControl() {
  const queryClient = useQueryClient();
  const storedKey = useSyncExternalStore(onCredentialChange, getApiKey);
  const [pending, setPending] = useState<AuthMode["mode"] | null>(null);
  const [failure, setFailure] = useState("");

  const { data, isPending } = useQuery({
    ...queries.authMode(),
  });

  const change = useMutation({
    mutationFn: setAuthMode,
    onSuccess: (updated) => {
      queryClient.setQueryData(["auth-mode"], updated);
      // The mode decides what every other request needs, so everything the
      // console has already read was read under the old one.
      void queryClient.invalidateQueries();
      setPending(null);
      setFailure("");
    },
    onError: (error) => {
      setPending(null);
      setFailure(
        error instanceof ApiError && error.message
          ? error.message
          : "The gateway refused the change.",
      );
    },
  });

  if (isPending) {
    return <p className="text-sm text-text-3">Reading the gateway setting…</p>;
  }
  if (!data) {
    return (
      <p className="text-sm text-text-3">
        This gateway did not report an authentication mode.
      </p>
    );
  }

  const locked = !data.can_change;

  return (
    <>
      <div
        role="radiogroup"
        aria-label="Gateway authentication"
        className="inline-flex rounded-sm border border-border-2 p-0.5"
      >
        {CHOICES.map(({ mode, label, icon: Icon }) => (
          <button
            key={mode}
            type="button"
            role="radio"
            aria-checked={data.mode === mode}
            disabled={locked || change.isPending}
            onClick={() => {
              if (data.mode === mode) return;
              setFailure("");
              setPending(mode);
            }}
            className={`flex h-8 items-center gap-1.5 rounded-xs px-3 text-sm transition-colors duration-150 ease-standard disabled:cursor-not-allowed disabled:opacity-50 ${
              data.mode === mode
                ? "bg-bg-hover text-text-1"
                : "text-text-3 hover:text-text-2"
            }`}
          >
            <Icon className="size-4" />
            {label}
          </button>
        ))}
      </div>
      <p className="mt-3 text-sm text-text-3" aria-live="polite">
        {failure ? (
          <span className="text-error">{failure}</span>
        ) : locked ? (
          // The read and the write share one refusal in the gateway, so this
          // is the same sentence the switch would have answered with.
          (data.reason ??
          "This gateway does not allow the mode to be changed from here.")
        ) : data.mode === "disabled" ? (
          "No key required. Requests are metered against the default account."
        ) : (
          "Every request needs a gateway API key."
        )}
      </p>
      {pending && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setPending(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{
            pending === "required"
              ? "Require an API key"
              : "Serve this gateway without a key"
          }</DialogTitle>
            </DialogHeader>
            <DialogBody>
              <p className="text-sm text-text-2">
                {consequence(pending, storedKey !== "")}
              </p>
            </DialogBody>
            <DialogFooter>
              <GhostButton onClick={() => setPending(null)}>Cancel</GhostButton>
              <PrimaryButton
                onClick={() => change.mutate(pending)}
                disabled={change.isPending}
              >
                {change.isPending
                  ? "Applying…"
                  : pending === "required"
                    ? "Require a key"
                    : "Turn authentication off"}
              </PrimaryButton>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
