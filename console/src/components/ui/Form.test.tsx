// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { Field, INPUT_CLASS } from "./Form";

afterEach(cleanup);

test("a field with an error marks its control invalid and links the message", () => {
  render(
    <Field label="Name" hint="What people call it." error="Name is taken.">
      <input className={INPUT_CLASS} defaultValue="acme" />
    </Field>,
  );

  const input = screen.getByLabelText("Name", { selector: "input" });
  expect(input.getAttribute("aria-invalid")).toBe("true");
  const message = screen.getByRole("alert");
  expect(message.textContent).toBe("Name is taken.");
  const described = (input.getAttribute("aria-describedby") ?? "").split(" ");
  expect(described).toContain(message.id);
  expect(described).toContain(screen.getByText("What people call it.").id);
});

test("a field without an error leaves the control valid", () => {
  render(
    <Field label="Name" required>
      <input className={INPUT_CLASS} />
    </Field>,
  );

  const input = screen.getByLabelText(/Name/, { selector: "input" });
  expect(input.getAttribute("aria-invalid")).toBeNull();
  expect(input.getAttribute("aria-required")).toBe("true");
  expect(screen.queryByRole("alert")).toBeNull();
});
