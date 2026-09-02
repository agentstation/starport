import { describe, expect, it } from "vitest";

import { ACTIVITY_RECORD_KEYS } from "@/lib/api";
import goUsageModel from "../../../internal/usage/model.go?raw";

// The record type is a hand-written mirror of the Go struct, so the only
// thing that keeps the two in step is this comparison. A key the gateway
// never writes would render as an absent value on every row and read as a
// gateway bug.
describe("ActivityRecord keys", () => {
  it("each names a JSON tag on the Go usage record", () => {
    const tags = new Set(
      [...goUsageModel.matchAll(/json:"([a-z_]+)(?:,omitempty)?"/g)].map((match) => match[1]),
    );
    const missing = ACTIVITY_RECORD_KEYS.filter((key) => !tags.has(key));
    expect(missing).toEqual([]);
  });
});
