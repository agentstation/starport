// Timing labels state what a console timing measures. A timing is complete
// only when it covers the whole request path that the reader sees. A
// component timing, such as the gateway service time, is partial, and the
// console never shows it as a request timing. The gateway names the boundary
// of each metric it reports; the console only words it.

// Boundary codes. The gateway owns the first two in internal/usage
// (TimingGatewayService and TimingGatewayAdded). The browser measures the
// third itself.
export const GATEWAY_SERVICE = "gateway_service";
export const GATEWAY_ADDED = "gateway_added";
export const BROWSER_ROUND_TRIP = "browser_round_trip";

// TIMING_BOUNDARY gives each boundary code a short name and one sentence.
export const TIMING_BOUNDARY: Record<string, { name: string; sentence: string }> = {
  [GATEWAY_SERVICE]: {
    name: "gateway service",
    sentence:
      "From the start of the gateway service to the response or the stream end. Authentication, limits, budgets, and request decoding are outside.",
  },
  [GATEWAY_ADDED]: {
    name: "gateway added",
    sentence: "The gateway service time minus the provider waits inside it. The same stages are outside.",
  },
  [BROWSER_ROUND_TRIP]: {
    name: "browser round trip",
    sentence: "From the browser request to the last byte. It includes the network and every gateway stage.",
  },
};

// Timing is the boundary part of a metric that the gateway reports.
export type Timing = { boundary?: string; complete?: boolean };

export type TimingLabel = {
  complete: boolean;
  // text is the visible label, for example "Partial: gateway service".
  text: string;
  // sentence states the boundary in full.
  sentence: string;
};

// timingLabel words one timing. Only a timing that says it is complete is
// complete. A timing with no boundary comes from an older gateway, so the
// label says that the boundary is not known. A truncated sample makes every
// value from it partial, whatever its boundary.
export function timingLabel(timing: Timing | undefined, sample?: { truncated?: boolean }): TimingLabel {
  const known = timing?.boundary ? TIMING_BOUNDARY[timing.boundary] : undefined;
  const complete = timing?.complete === true && known !== undefined && sample?.truncated !== true;
  const name = known?.name ?? timing?.boundary ?? "boundary not reported";
  const parts = [`${complete ? "Complete" : "Partial"}: ${name}`];
  if (sample?.truncated) parts.push("partial sample");
  const sentences = [known?.sentence ?? "The gateway does not report what this timing measures."];
  if (sample?.truncated) sentences.push("The sample holds only the newest requests.");
  return { complete, text: parts.join(", "), sentence: sentences.join(" ") };
}
