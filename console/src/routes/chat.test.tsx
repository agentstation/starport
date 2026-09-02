// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

// CPL-F4. A new conversation opens on a model that can answer, and the picker
// lists only the models that answer a chat turn. The catalog below puts an
// embedding model first so that catalog order alone cannot pass, and it gives
// the usable credential to the provider of the second chat model so that the
// first chat model alone cannot pass either.
const MODELS = {
  data: [
    {
      id: "openai/text-embedding-3-small",
      name: "Embedding 3 small",
      offerings: [
        {
          provider: "openai",
          provider_model_id: "text-embedding-3-small",
          operations: ["embeddings"],
        },
      ],
    },
    {
      id: "cohere/rerank-v3.5",
      name: "Rerank 3.5",
      offerings: [
        { provider: "cohere", provider_model_id: "rerank-v3.5", operations: ["rerank"] },
      ],
    },
    {
      id: "anthropic/claude-fable-5",
      name: "Claude Fable 5",
      offerings: [
        {
          provider: "anthropic",
          provider_model_id: "claude-fable-5",
          operations: ["chat-completions"],
        },
      ],
    },
    {
      id: "openai/gpt-x",
      name: "GPT-X",
      offerings: [
        { provider: "openai", provider_model_id: "gpt-x", operations: ["chat-completions"] },
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

const LAST_MODEL_KEY = "starport.lastModel";

// stubGateway installs the in-memory localStorage the route reads, so the
// remembered model is written after it and cleared with it.
beforeEach(() => {
  stubGateway({
    "/api/v1/models": MODELS,
    "/api/v1/admin/providers": STATUS,
    "/api/v1/presets": { data: [] },
    "/api/v1/providers": { providers: [] },
  });
});

afterEach(resetGateway);

async function modelButton() {
  return screen.findByRole("button", { name: "Choose model" });
}

test("a new conversation opens on the first chat model a credentialed provider serves", async () => {
  openConsole("/chat");
  const button = await modelButton();
  await waitFor(() => expect(button.textContent).toMatch(/gpt-x/));
  expect(button.textContent).not.toMatch(/embedding/i);
});

test("the model the reader chose last wins while it still routes chat", async () => {
  localStorage.setItem(LAST_MODEL_KEY, "anthropic/claude-fable-5");
  openConsole("/chat");
  const button = await modelButton();
  await waitFor(() => expect(button.textContent).toMatch(/claude-fable-5/));
  // The catalog answered and the rule left the choice alone. The picker
  // holds the remembered model as selected once the catalog is in.
  fireEvent.click(button);
  const listbox = await screen.findByRole("listbox");
  const selected = await within(listbox).findByRole("option", { selected: true });
  expect(selected.textContent).toContain("anthropic/claude-fable-5");
});

test("a remembered model that answers no chat turn is replaced", async () => {
  localStorage.setItem(LAST_MODEL_KEY, "openai/text-embedding-3-small");
  openConsole("/chat");
  const button = await modelButton();
  await waitFor(() => expect(button.textContent).toMatch(/gpt-x/));
});

test("the picker lists the models that answer a chat turn and no other", async () => {
  openConsole("/chat");
  const button = await modelButton();
  await waitFor(() => expect(button.textContent).toMatch(/gpt-x/));
  fireEvent.click(button);
  const listbox = await screen.findByRole("listbox");
  const options = await within(listbox).findAllByRole("option");
  const ids = options.map((option) => option.textContent ?? "");
  expect(ids.some((text) => text.includes("openai/gpt-x"))).toBe(true);
  expect(ids.some((text) => text.includes("anthropic/claude-fable-5"))).toBe(true);
  expect(ids.some((text) => text.includes("text-embedding"))).toBe(false);
  expect(ids.some((text) => text.includes("rerank"))).toBe(false);
});
