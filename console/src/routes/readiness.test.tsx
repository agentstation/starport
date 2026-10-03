// @vitest-environment jsdom
import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

// Explore lists every catalog model, and each chat action says whether it
// can get an answer. Anthropic serves Claude Fable 5 but holds no usable
// credential, and OpenAI serves GPT-X with one.
const MODELS = {
  data: [
    {
      id: "anthropic/claude-fable-5",
      name: "Claude Fable 5",
      offerings: [
        {
          provider: "anthropic",
          provider_name: "Anthropic",
          provider_model_id: "claude-fable-5",
          operations: ["chat-completions"],
        },
      ],
    },
    {
      id: "openai/gpt-x",
      name: "GPT-X",
      offerings: [
        {
          provider: "openai",
          provider_name: "OpenAI",
          provider_model_id: "gpt-x",
          operations: ["chat-completions"],
        },
      ],
    },
  ],
};

const STATUS = {
  providers: [
    { provider_id: "anthropic", operator_credential: { usable: false } },
    { provider_id: "openai", operator_credential: { usable: true } },
  ],
};

function gateway(status: object = STATUS, models: object = MODELS) {
  stubGateway({
    "/api/v1/models": models,
    "/api/v1/admin/providers": status,
    "/api/v1/presets": { data: [] },
    "/api/v1/providers": { providers: [] },
  });
}

afterEach(resetGateway);

test("a model without a usable credential stays in Explore", async () => {
  gateway();
  openConsole("/models");

  expect(await screen.findByText("Claude Fable 5")).toBeTruthy();
  expect(screen.getByText("GPT-X")).toBeTruthy();
});

test("the model page says why chat on a model without a credential fails", async () => {
  gateway();
  openConsole("/models/anthropic%2Fclaude-fable-5");

  const note = await screen.findByTestId("chat-readiness");
  await waitFor(() => expect(note.getAttribute("data-state")).toBe("no_credential"));
  expect(note.textContent).toMatch(/No provider that serves this model has a usable credential/);
});

test("the chat composer names the provider that can answer", async () => {
  gateway();
  openConsole("/chat");

  const note = await screen.findByTestId("chat-readiness");
  await waitFor(() => expect(note.getAttribute("data-state")).toBe("ready"));
  expect(note.textContent).toBe("Ready: OpenAI can answer chat completions with a usable credential.");
});

test("chat says readiness is not known when the key cannot read credentials", async () => {
  gateway({});
  openConsole("/chat");

  const note = await screen.findByTestId("chat-readiness");
  await waitFor(() => expect(note.getAttribute("data-state")).toBe("unknown"));
  expect(note.textContent).toMatch(/readiness is not known/);
});

test("chat says it cannot send when the gateway serves no catalog models", async () => {
  gateway(STATUS, { data: [] });
  openConsole("/chat");

  const note = await screen.findByTestId("chat-readiness");
  await waitFor(() => expect(note.getAttribute("data-state")).toBe("unavailable"));
  expect(note.textContent).toMatch(/serves no catalog models yet, so chat cannot send/);
});

test("compare states readiness for each model", async () => {
  gateway();
  openConsole("/chat?model=anthropic%2Fclaude-fable-5&compare=true");

  await waitFor(() => expect(screen.getAllByTestId("chat-readiness").length).toBeGreaterThan(0));
  const notes = screen.getAllByTestId("chat-readiness");
  const fable = notes.find((note) => within(note).queryByText(/anthropic\/claude-fable-5/));
  expect(fable?.getAttribute("data-state")).toBe("no_credential");
});
