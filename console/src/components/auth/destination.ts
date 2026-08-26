// AUTH_PATH is where a reader with no usable credential meets this gateway. It
// is the one route that renders outside the shell, because a shell is
// navigation to pages that would all answer 401.
export const AUTH_PATH = "/auth";

// destination is where a reader goes once the session opens.
//
// Only a path on this origin is accepted. `next` arrives in a URL, so it is
// whatever the last link said; honouring a full URL would make the console a
// redirector — follow a link, open your own gateway, land on somebody else's
// page carrying the confidence of having just presented a credential. A leading
// `//` is rejected for the same reason: browsers read it as a host.
export function destination(next: string | undefined): string {
  if (!next) return "/";
  if (!next.startsWith("/") || next.startsWith("//")) return "/";
  if (next.startsWith(AUTH_PATH)) return "/";
  return next;
}
