// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { EntityLogo, entityInitials } from "./EntityLogo";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function stubFetch(response: { ok: boolean; body?: string }) {
  const mock = vi.fn(() =>
    Promise.resolve({
      ok: response.ok,
      text: () => Promise.resolve(response.body ?? ""),
    }),
  );
  vi.stubGlobal("fetch", mock);
  return mock;
}

test("renders the bundled SVG inline when the gateway serves one", async () => {
  const mock = stubFetch({
    ok: true,
    body: '<svg fill="currentColor" viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>',
  });
  render(<EntityLogo kind="providers" id="openai" name="OpenAI" />);

  await waitFor(() => {
    expect(screen.getByTestId("entity-mark").querySelector("svg")).not.toBeNull();
  });
  expect(mock).toHaveBeenCalledWith("/api/v1/logos/providers/openai.svg");
});

test("tints monochrome marks through the theme text color", async () => {
  stubFetch({
    ok: true,
    body: '<svg fill="currentColor" viewBox="0 0 24 24"><path d="M0 0h24z"/></svg>',
  });
  render(<EntityLogo kind="providers" id="groq" name="Groq" />);

  await waitFor(() => {
    expect(screen.getByTestId("entity-mark").querySelector("svg")).not.toBeNull();
  });
  // The wrapper carries the theme text color; the inlined currentColor
  // mark inherits it. An <img> tag could not do this.
  expect(screen.getByTestId("entity-mark").className).toContain("text-text-1");
});

test("tints a mark that names no fill at all", async () => {
  // The catalog can carry a bare glyph (models.dev ships raw
  // simple-icons paths); without a tint it renders the SVG default —
  // ink-black in every theme.
  stubFetch({
    ok: true,
    body: '<svg viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>',
  });
  render(<EntityLogo kind="providers" id="hetzner" name="Hetzner" />);

  await waitFor(() => {
    expect(screen.getByTestId("entity-mark").querySelector("svg")).not.toBeNull();
  });
  expect(screen.getByTestId("entity-mark").className).toContain("fill-current");
});

test("leaves a declared fill alone", async () => {
  stubFetch({
    ok: true,
    body: '<svg viewBox="0 0 24 24"><path fill="#D50C2D" d="M0 0h24v24H0z"/></svg>',
  });
  render(<EntityLogo kind="providers" id="fireworks-ai" name="Fireworks" />);

  await waitFor(() => {
    expect(screen.getByTestId("entity-mark").querySelector("svg")).not.toBeNull();
  });
  expect(screen.getByTestId("entity-mark").className).not.toContain(
    "fill-current",
  );
});

test("falls back to two-letter initials when no mark is bundled", async () => {
  stubFetch({ ok: false });
  render(<EntityLogo kind="authors" id="bria" name="Bria AI" />);

  await waitFor(() => {
    expect(screen.getByTestId("entity-initials").textContent).toBe("BA");
  });
});

test("falls back to initials when the fetch itself fails", async () => {
  vi.stubGlobal("fetch", vi.fn(() => Promise.reject(new Error("offline"))));
  render(<EntityLogo kind="authors" id="reka" name="Reka" />);

  await waitFor(() => {
    expect(screen.getByTestId("entity-initials").textContent).toBe("RE");
  });
});

test("fetches each mark once across many cards", async () => {
  const mock = stubFetch({ ok: true, body: "<svg viewBox='0 0 24 24'/>" });
  render(
    <>
      <EntityLogo kind="providers" id="mistral" name="Mistral" />
      <EntityLogo kind="providers" id="mistral" name="Mistral" />
    </>,
  );

  await waitFor(() => {
    expect(screen.getAllByTestId("entity-mark")).toHaveLength(2);
  });
  expect(mock).toHaveBeenCalledTimes(1);
});

test("derives initials from word boundaries", () => {
  expect(entityInitials("Black Forest Labs")).toBe("BF");
  expect(entityInitials("moonshot-ai")).toBe("MA");
  expect(entityInitials("Groq")).toBe("GR");
  expect(entityInitials("  ")).toBe("?");
});
