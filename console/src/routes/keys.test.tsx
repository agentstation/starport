// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { json, openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);
afterEach(cleanup);

const MODELS = {
  data: [
    { id: "openai/gpt-x", name: "GPT-X" },
    { id: "anthropic/claude-fable-5", name: "Claude Fable 5" },
    { id: "meta/llama-3.1-8b-instruct", name: "Llama" },
  ],
};

describe("key form", () => {
  // The allowed models field is the catalog picker, so an operator picks
  // an ID the gateway serves instead of typing one from memory.
  it("opens the catalog picker for allowed models from the create form", async () => {
    stubGateway({
      "/api/v1/admin/keys": { keys: [] },
      "/api/v1/models": MODELS,
      "/api/v1/admin/keys/": () => json({}),
    });
    openConsole("/keys");

    fireEvent.click(await screen.findByRole("button", { name: /New key/ }));
    const dialog = await screen.findByRole("dialog", { name: "New API key" });
    const picker = within(dialog).getByRole("combobox", { name: "Allowed models" });
    fireEvent.focus(picker);
    fireEvent.change(picker, { target: { value: "gpt" } });

    const list = await within(dialog).findByRole("listbox");
    const options = within(list).getAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual(["openai/gpt-x"]);

    fireEvent.click(within(options[0]!).getByRole("button"));
    expect(within(dialog).getByRole("button", { name: "Remove openai/gpt-x" })).toBeTruthy();
    // The pick clears the draft, which closes the list until the next keystroke.
    expect(picker.getAttribute("aria-expanded")).toBe("false");
    fireEvent.change(picker, { target: { value: "a" } });
    const remaining = within(within(dialog).getByRole("listbox"))
      .getAllByRole("option")
      .map((option) => option.textContent);
    expect(remaining).toEqual(["anthropic/claude-fable-5", "meta/llama-3.1-8b-instruct"]);
  });
});

describe("scope pills", () => {
  it("reads the admin scope as every scope and folds the rest past four into a count", async () => {
    const { ScopePills } = await import("./keys");
    render(
      <>
        <div data-testid="admin">
          <ScopePills scopes={["admin"]} />
        </div>
        <div data-testid="many">
          <ScopePills
            scopes={["chat:write", "embeddings:write", "images:write", "audio:write", "rerank:write", "models:read"]}
          />
        </div>
        <div data-testid="wildcard">
          <ScopePills scopes={["*"]} />
        </div>
        <div data-testid="few">
          <ScopePills scopes={["chat:write", "models:read"]} />
        </div>
      </>,
    );
    expect(screen.getByTestId("admin").textContent).toBe("all scopes");
    expect(screen.getByTestId("wildcard").textContent).toBe("all scopes");
    const many = screen.getByTestId("many");
    expect(many.textContent).toBe("chat:writeembeddings:writeimages:writeaudio:write+2");
    expect(
      within(many).getByRole("button", { name: "2 more scopes: rerank:write, models:read" }),
    ).toBeTruthy();
    expect(screen.getByTestId("few").textContent).toBe("chat:writemodels:read");
  });
});
