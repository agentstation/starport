// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import type { ChatMessage } from "@/lib/chatStore";

import { AssistantMessage } from "./Messages";

// Streamdown loads Shiki, KaTeX, and Mermaid. None of them decide whether a
// generated picture reaches the transcript, and all three are slow to start
// under jsdom, so the markdown body is stubbed and the media half is real.
vi.mock("streamdown", () => ({
  Streamdown: ({ children }: { children: string }) => <div>{children}</div>,
}));
vi.mock("@streamdown/code", () => ({ code: {} }));
vi.mock("@streamdown/math", () => ({ math: {} }));
vi.mock("@streamdown/mermaid", () => ({ mermaid: {} }));

afterEach(cleanup);

const PIXEL =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

function assistantTurn(generated: ChatMessage["generated"]): ChatMessage {
  return {
    role: "assistant",
    content: "Here is the picture you asked for.",
    model: "openai/gpt-image-1",
    generated,
  };
}

test("a finished turn announces itself once through the live region", () => {
  const message: ChatMessage = {
    role: "assistant",
    content: "The answer.",
    model: "openai/gpt-oss-120b",
  };
  const { rerender } = render(
    <AssistantMessage message={message} streaming retryModels={[]} />,
  );

  const region = document.querySelector('[aria-live="polite"]');
  if (!region) throw new Error("no live region");
  expect(region.textContent).toBe("");

  rerender(<AssistantMessage message={message} streaming={false} retryModels={[]} />);
  expect(region.textContent).toBe("Response finished from openai/gpt-oss-120b.");
});

test("a turn holding a generated image renders an image element", () => {
  render(
    <AssistantMessage
      message={assistantTurn([{ kind: "image", url: PIXEL }])}
      streaming={false}
      retryModels={[]}
    />,
  );

  const image = screen.getByAltText("Generated image 1");
  expect(image.tagName).toBe("IMG");
  expect(image.getAttribute("src")).toBe(PIXEL);
});

test("a spoken answer gets a player and prints its transcript", () => {
  render(
    <AssistantMessage
      message={assistantTurn([
        {
          kind: "audio",
          url: "data:audio/mp3;base64,SUQz",
          transcript: "the spoken sentence",
        },
      ])}
      streaming={false}
      retryModels={[]}
    />,
  );

  const player = screen.getByLabelText("Generated audio 1");
  expect(player.tagName).toBe("AUDIO");
  // A reader who cannot listen still reads what the model said.
  expect(screen.getByText("the spoken sentence")).toBeTruthy();
});

test("a text answer renders no media element", () => {
  render(
    <AssistantMessage
      message={assistantTurn(undefined)}
      streaming={false}
      retryModels={[]}
    />,
  );

  expect(document.querySelector("img")).toBeNull();
  expect(document.querySelector("audio")).toBeNull();
});

// CPL-F4. A reader scanning a transcript tells the makers apart by their
// marks before reading an id. The mark names the catalog author, and a
// catalog with no bundled mark falls back to initials.
test("an assistant turn carries the maker mark beside the model name", async () => {
  vi.stubGlobal("fetch", vi.fn(() => Promise.reject(new Error("offline"))));
  try {
    render(
      <AssistantMessage
        message={{ role: "assistant", content: "The answer.", model: "openai/gpt-oss-120b" }}
        streaming={false}
        retryModels={[]}
        modelRecord={{
          id: "openai/gpt-oss-120b",
          authors: [{ id: "openai", name: "OpenAI" }],
        }}
      />,
    );
    const mark = await screen.findByTestId("entity-initials");
    expect(mark.textContent).toBe("OP");
    expect(screen.getByText("openai/gpt-oss-120b")).toBeTruthy();
  } finally {
    vi.unstubAllGlobals();
  }
});
