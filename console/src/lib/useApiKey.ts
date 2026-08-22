import { useSyncExternalStore } from "react";

import { getApiKey, isApiKeyRejected, onKeyChange } from "./api";

// useApiKeyRejected reports that the gateway refused the stored key. It stays
// false until a request actually fails, so a page renders normally until the
// gateway says otherwise.
export function useApiKeyRejected(): boolean {
  return useSyncExternalStore(onKeyChange, isApiKeyRejected);
}

// useApiKeyUsable gates the surfaces that need a working key. A stored key the
// gateway rejects is no more usable than no key at all, so both states send
// the reader to the connect prompt rather than to a permission error.
export function useApiKeyUsable(): boolean {
  return useSyncExternalStore(
    onKeyChange,
    () => Boolean(getApiKey()) && !isApiKeyRejected(),
  );
}
