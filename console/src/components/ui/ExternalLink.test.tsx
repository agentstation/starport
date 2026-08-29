import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";

import { ExternalLink } from "./ExternalLink";
import { GitHubMark } from "./icons";

test("external links open a new tab and carry the new-tab glyph", () => {
  render(<ExternalLink href="https://example.com">Website</ExternalLink>);
  const anchor = screen.getByRole("link", { name: "Website" });
  expect(anchor.getAttribute("href")).toBe("https://example.com");
  expect(anchor.getAttribute("target")).toBe("_blank");
  expect(anchor.getAttribute("rel")).toBe("noreferrer");
  expect(screen.getByTestId("new-tab-icon")).toBeTruthy();
});

test("a leading icon renders inside the anchor before the label", () => {
  render(
    <ExternalLink href="https://github.com/agentstation" icon={GitHubMark}>
      GitHub
    </ExternalLink>,
  );
  const anchor = screen.getByRole("link", { name: "GitHub" });
  expect(anchor.querySelectorAll("svg")).toHaveLength(2);
});
