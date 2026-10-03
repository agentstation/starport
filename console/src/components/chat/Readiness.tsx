import type { ChatReadiness } from "@/lib/modelFilter";
import { cn } from "@/lib/utils";

const TONE: Record<ChatReadiness["state"], string> = {
  ready: "text-text-2",
  unknown: "text-text-2",
  no_credential: "text-warning",
  unavailable: "text-warning",
};

// ReadinessNote states whether a chat action on one model can get an
// answer for this caller. chatReadiness owns the rule.
export function ReadinessNote({
  readiness,
  model,
  className,
}: {
  readiness: ChatReadiness;
  // model names the model when the note sits beside several.
  model?: string;
  className?: string;
}) {
  return (
    <p data-testid="chat-readiness" data-state={readiness.state} className={cn("text-sm", TONE[readiness.state], className)}>
      {model && <span className="font-mono">{model}: </span>}
      {readiness.text}
    </p>
  );
}
