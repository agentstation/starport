import { describe, expect, it } from "vitest";

import { DEFAULT_PARAMS, requestHeaders, SEMANTIC_CACHE_HEADER, statsFromUsage } from "./chatStore";

describe("chat request headers", () => {
  // The semantic cache is opt-in per request: the header rides only when
  // the reader ticked the box, so an ordinary turn never asks for a
  // near-duplicate answer.
  it("sends X-Semantic-Cache only when the parameter is on", () => {
    expect(requestHeaders(DEFAULT_PARAMS)).toEqual({});
    expect(requestHeaders({ ...DEFAULT_PARAMS, semanticCache: true })).toEqual({
      [SEMANTIC_CACHE_HEADER]: "true",
    });
  });

  // A semantic hit reports its similarity beside the cache status, so the
  // stat line separates a near-duplicate answer from a verbatim replay.
  it("keeps the cache similarity the gateway reported", () => {
    const stats = statsFromUsage(null, { ttftMs: 10, latencyMs: 20 }, { cache: "HIT", cacheSimilarity: "0.93" });
    expect(stats.cacheSimilarity).toBe("0.93");
  });
});
