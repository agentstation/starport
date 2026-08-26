// TrustScope is what this page can honestly say about the address the reader
// reached it on.
//
// It is derived from the location rather than written into the copy. A fixed
// "Local-only" would be a claim the page cannot check, and it would be wrong in
// exactly the case where being wrong costs something: a gateway bound to a
// network address, where the reader is about to paste the admin token for
// somebody else's machine into a page that arrived over a hop they do not
// control.
export type TrustScope = {
  // local is true when this page was served by the machine reading it.
  local: boolean;
  // label is the one-line readout beside the form.
  label: string;
  // detail says what the label means for the token the reader is holding.
  detail: string;
};

// LOOPBACK is the set of hostnames that can only mean this machine. A name that
// merely resolves to a loopback address is not on the list: this page cannot
// resolve anything, and a scope readout that guessed would be asserting more
// than it knows.
const LOOPBACK = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);

export function trustScope(hostname: string, secure: boolean): TrustScope {
  if (LOOPBACK.has(hostname)) {
    return {
      local: true,
      label: `Local-only · ${hostname}`,
      detail:
        "This page came from a loopback address, so the gateway is running on the machine you are sitting at and nothing off it can reach this port.",
    };
  }
  const host = hostname || "an unknown address";
  if (secure) {
    return {
      local: false,
      label: `Network · ${host}`,
      detail:
        "This is not a loopback address, so this gateway is reachable from beyond the machine running it. The connection is encrypted, but the token below is still that machine's admin credential — paste it only if you trust this address.",
    };
  }
  return {
    local: false,
    label: `Network · ${host} · not encrypted`,
    detail:
      "This is not a loopback address and the connection is not encrypted, so anything pasted here crosses the network in the clear. The token below is the admin credential for the machine running this gateway.",
  };
}
