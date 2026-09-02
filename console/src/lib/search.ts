// Search param readers. Each route owns its schema, and every schema reads
// a value the same way: a non-empty string counts, anything else reads as
// absent. A value outside a named set reads as absent too, so a stale or
// hand-edited address falls back to the route default instead of failing.

export function optionalString(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

export function oneOf<const T extends readonly string[]>(
  values: T,
  value: unknown,
): T[number] | undefined {
  return typeof value === "string" && (values as readonly string[]).includes(value)
    ? (value as T[number])
    : undefined;
}
