import { useSyncExternalStore } from "react";

import { getApiKey, onKeyChange } from "./api";

// useHasApiKey re-renders subscribers when the stored key changes, so
// locked surfaces unlock the moment a key is set.
export function useHasApiKey(): boolean {
  return useSyncExternalStore(onKeyChange, () => Boolean(getApiKey()));
}
