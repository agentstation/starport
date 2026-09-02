import { afterEach, beforeEach, expect, test, vi } from "vitest";

import {
  accessMessage,
  ApiError,
  isCredentialRejected,
  request,
  setApiKey,
  streamChat,
} from "./api";

function respond(status: number): Response {
  return new Response(status === 204 ? null : JSON.stringify({}), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Node exposes an unavailable `localStorage` global that shadows the jsdom
// one, so the browser store the client reads is stubbed here.
function memoryStorage(): Storage {
  const entries = new Map<string, string>();
  return {
    get length() {
      return entries.size;
    },
    clear: () => entries.clear(),
    getItem: (key: string) => entries.get(key) ?? null,
    key: (index: number) => [...entries.keys()][index] ?? null,
    removeItem: (key: string) => void entries.delete(key),
    setItem: (key: string, value: string) => void entries.set(key, value),
  };
}

beforeEach(() => {
  vi.stubGlobal("localStorage", memoryStorage());
  setApiKey("starport-test-key");
});

afterEach(() => {
  setApiKey("");
  vi.unstubAllGlobals();
});

// The gateway answers an unknown key with 401 and an insufficient scope with
// 403. Reporting the first as the second sends the reader hunting for a
// permission they already hold, which is what a stale development key did.
test("accessMessage names the stored key for 401 and the scope for 403", () => {
  const unauthorized = accessMessage(new ApiError(401, "no", null), "admin");
  const forbidden = accessMessage(new ApiError(403, "no", null), "admin");

  expect(unauthorized).toMatch(/not accepted/i);
  expect(unauthorized).not.toMatch(/scope/i);
  expect(forbidden).toMatch(/admin scope/);
  expect(forbidden).not.toMatch(/not accepted/i);
});

test("a 401 marks the stored key rejected", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));

  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(true);
});

// The gateway answers a spent budget with 402 and names the budget in the
// message. The console reads the status, not the text, so a reworded message
// still renders as a budget, not as a generic failure.
test("a 402 parses to an exhausted budget", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            error: {
              type: "permission_error",
              message: "team budget is spent for this day",
            },
          }),
          { status: 402, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );

  const error = await request("/v1/chat/completions", { method: "POST" }).catch(
    (caught: unknown) => caught,
  );
  expect(error).toBeInstanceOf(ApiError);
  const apiError = error as ApiError;
  expect(apiError.budgetExhausted).toBe(true);
  expect(apiError.guardrailRefusal).toBe(false);
  expect(apiError.message).toBe("team budget is spent for this day");
  expect(isCredentialRejected()).toBe(false);
});

// A 403 proves the key authenticated, so it must not send the console back to
// the connect prompt; the reader would replace a key that works.
test("a 403 leaves the stored key usable", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(403)));

  await expect(request("/api/v1/admin/info")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(false);
});

test("a success clears an earlier rejection", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));
  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(true);

  vi.stubGlobal("fetch", vi.fn(async () => respond(200)));
  await request("/api/v1/models");
  expect(isCredentialRejected()).toBe(false);
});

test("storing a new key clears the rejection", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));
  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);

  setApiKey("starport-replacement-key");
  expect(isCredentialRejected()).toBe(false);
});

// Without a stored key a 401 says nothing about a key, so it must not set the
// rejected state that the reconnect prompt reads.
test("a 401 without a stored key does not mark a rejection", async () => {
  setApiKey("");
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));

  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(false);
});

// sseResponse answers one streaming request with the given events, one SSE
// frame each, split across two network reads so the line buffer is exercised.
function sseResponse(events: unknown[]): Response {
  const frames = events.map((event) => `data: ${JSON.stringify(event)}\n\n`);
  const body = `${frames.join("")}data: [DONE]\n\n`;
  const bytes = new TextEncoder().encode(body);
  const split = Math.floor(bytes.length / 2);
  return new Response(
    new ReadableStream({
      start(controller) {
        controller.enqueue(bytes.slice(0, split));
        controller.enqueue(bytes.slice(split));
        controller.close();
      },
    }),
    { status: 200, headers: { "Content-Type": "text/event-stream" } },
  );
}

// A spoken answer arrives in chunks, and each chunk is base64 on its own.
// Joining the encoded strings produces padding in the middle, which no
// decoder accepts, so the bytes are joined and encoded once at the end.
test("a chunked spoken answer joins its bytes, not its base64", async () => {
  const first = btoa("first-half-");
  const second = btoa("second-half");
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      sseResponse([
        { model: "openai/gpt-audio", choices: [{ delta: { content: "hi" } }] },
        {
          choices: [
            { delta: { audio: { data: first, format: "wav", transcript: "hi " } } },
          ],
        },
        {
          choices: [
            { delta: { audio: { data: second, transcript: "there" } } },
          ],
        },
      ]),
    ),
  );

  const meta = await streamChat({ model: "openai/gpt-audio" }, { onDelta: () => {} });

  expect(meta.media).toEqual([
    {
      kind: "audio",
      url: `data:audio/wav;base64,${btoa("first-half-second-half")}`,
      transcript: "hi there",
    },
  ]);
});

// A picture arrives whole in one delta and keeps the URL the gateway sent.
test("a generated image reaches the turn metadata", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      sseResponse([
        {
          choices: [
            {
              delta: {
                images: [{ image_url: { url: "data:image/png;base64,AAAA" } }],
              },
            },
          ],
        },
      ]),
    ),
  );

  const meta = await streamChat({ model: "openai/gpt-image-1" }, { onDelta: () => {} });

  expect(meta.media).toEqual([
    { kind: "image", url: "data:image/png;base64,AAAA" },
  ]);
});

// An unreadable chunk drops itself. The text of the same turn is already on
// screen, so failing the whole answer would lose more than it protects.
test("an unreadable audio chunk drops that chunk alone", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      sseResponse([
        { choices: [{ delta: { audio: { data: "!!!not-base64!!!" } } }] },
        { choices: [{ delta: { audio: { data: btoa("kept") } } }] },
      ]),
    ),
  );

  const meta = await streamChat({ model: "openai/gpt-audio" }, { onDelta: () => {} });

  expect(meta.media).toEqual([
    { kind: "audio", url: `data:audio/mp3;base64,${btoa("kept")}`, transcript: undefined },
  ]);
});

// A text answer produces no media at all, which is what most turns do.
test("a text answer carries no media", async () => {
  const chunks: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      sseResponse([
        { model: "groq/llama", choices: [{ delta: { content: "hello" } }] },
      ]),
    ),
  );

  const meta = await streamChat(
    { model: "groq/llama" },
    { onDelta: (text) => chunks.push(text) },
  );

  expect(chunks.join("")).toBe("hello");
  expect(meta.model).toBe("groq/llama");
  expect(meta.media).toEqual([]);
});
