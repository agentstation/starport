import { expect, test } from "vitest";

// BYOK names one thing: a provider credential an account brings for itself.
// Three other things sit near it — a gateway API key, the credential an
// operator applies for the whole deployment, and the credential this process
// read from its environment — and the console's job is to keep a reader from
// confusing any of them with BYOK.
//
// The word is checked against the source rather than against a render because
// absence is the claim, and a render test only proves the word was missing
// from whatever the fixture happened to draw. Reading the source proves it is
// missing from every state those screens can reach, including the ones no
// fixture exercises.
const SOURCE = import.meta.glob("../**/*.{ts,tsx}", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

function saysByok(path: string): boolean {
  const text = SOURCE[path];
  if (text === undefined) throw new Error(`no such source file: ${path}`);
  return /byok/i.test(text);
}

test("BYOK is the word for a credential an account brings, and for nothing else", () => {
  // The keys screen lost its BYOK section: a gateway API key never carries a
  // provider credential, and the section keyed by one is what taught otherwise.
  expect(saysByok("../routes/keys.tsx")).toBe(false);

  // The provider screens show the two credentials an operator owns. Naming
  // either of them BYOK would put a tenant's word on the deployment's key.
  const providerScreens = Object.keys(SOURCE)
    .filter((path) => path.startsWith("../components/providers/"))
    .filter(saysByok);
  expect(providerScreens).toEqual([]);

  // And the account plane keeps it.
  expect(saysByok("../components/credentials/ByokPanel.tsx")).toBe(true);
});
