// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, test } from "vitest";

import { TokenInput } from "./TokenInput";

afterEach(cleanup);

function Harness({ initial = [] as string[] }) {
  const [values, setValues] = useState<string[]>(initial);
  return (
    <>
      <TokenInput values={values} onChange={setValues} aria-label="Stop sequences" />
      <output data-testid="values">{JSON.stringify(values)}</output>
    </>
  );
}

const values = () => JSON.parse(screen.getByTestId("values").textContent ?? "[]");

test("Enter and a comma commit tokens, and Backspace on an empty draft removes the last", () => {
  render(<Harness />);
  const input = screen.getByLabelText("Stop sequences");

  fireEvent.change(input, { target: { value: "END" } });
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.change(input, { target: { value: "STOP" } });
  fireEvent.keyDown(input, { key: "," });
  expect(values()).toEqual(["END", "STOP"]);

  fireEvent.keyDown(input, { key: "Backspace" });
  expect(values()).toEqual(["END"]);
});

test("a pasted comma list becomes several tokens and a chip's button removes it", () => {
  render(<Harness initial={["a"]} />);
  const input = screen.getByLabelText("Stop sequences");

  fireEvent.paste(input, { clipboardData: { getData: () => "b, c,a" } });
  expect(values()).toEqual(["a", "b", "c"]);

  fireEvent.click(screen.getByRole("button", { name: "Remove b" }));
  expect(values()).toEqual(["a", "c"]);
});
