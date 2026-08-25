import { useSyncExternalStore } from "react";

import { hasCredential, isCredentialRejected, onCredentialChange } from "./api";

// useGatewayAccessRejected reports that the gateway refused whatever this
// browser presented. It stays false until a request actually fails, so a page
// renders normally until the gateway says otherwise.
export function useGatewayAccessRejected(): boolean {
  return useSyncExternalStore(onCredentialChange, isCredentialRejected);
}

// useGatewayAccess gates the surfaces that need the gateway to answer. Access
// comes from either of two unlike things — a console session opened by
// `starport ui`, or a gateway API key pasted into this browser — and a
// credential the gateway refuses is no more usable than none at all, so both
// unusable states send the reader to the connect prompt rather than to a
// permission error.
export function useGatewayAccess(): boolean {
  return useSyncExternalStore(
    onCredentialChange,
    () => hasCredential() && !isCredentialRejected(),
  );
}
