// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";

import { QuickstartCard } from "./QuickstartCard";

afterEach(cleanup);

test("the curl snippet renders whole at once and colors its tokens when Shiki is ready", async () => {
  render(
    <TooltipProvider>
      <QuickstartCard />
    </TooltipProvider>,
  );

  const code = screen.getByTestId("snippet-curl");
  // Plain text first: the snippet never waits on the highlighter.
  expect(code.textContent).toContain(`curl ${location.origin}/v1/chat/completions`);
  expect(code.textContent).toContain("Authorization: Bearer $STARPORT_API_KEY");

  await waitFor(() => {
    expect(code.querySelectorAll('[data-testid="snippet-line"]').length).toBe(4);
  });
  // Tokens carry a light color and a dark color; the theme picks one.
  const token = code.querySelector('[data-testid="snippet-line"] > span') as HTMLElement;
  expect(token.style.getPropertyValue("--sdm-c")).toMatch(/^#/);
  expect(token.style.getPropertyValue("--shiki-dark")).toMatch(/^#/);
  // The text is unchanged by highlighting.
  expect(code.textContent).toContain("Authorization: Bearer $STARPORT_API_KEY");
});
