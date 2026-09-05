// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";

import { EndpointsCard } from "./EndpointsCard";

afterEach(cleanup);

test("each endpoint label is the copy control of its row, stated once", async () => {
  const writeText = vi.fn(async () => {});
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
  render(
    <TooltipProvider>
      <EndpointsCard />
    </TooltipProvider>,
  );

  const buttons = screen.getAllByRole("button");
  expect(buttons.map((button) => button.getAttribute("aria-label"))).toEqual([
    "Copy OpenAI SDK",
    "Copy OpenRouter SDK",
    "Copy Health",
  ]);
  // The name appears once per row: on the button, not beside it too.
  expect(screen.getAllByText("OpenAI SDK")).toHaveLength(1);

  fireEvent.click(buttons[0]!);
  expect(writeText).toHaveBeenCalledWith(`${location.origin}/v1`);
  expect(await screen.findByText("Copied")).toBeTruthy();
});
