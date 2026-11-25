import { useCallback, useSyncExternalStore } from "react";

// Features an administrator can switch on or off for this browser. Automations
// are a plugin that does not exist yet (docs/design-system.md), so they start
// off and the dock hides them until the setting is on. The choice is kept in
// this browser; a hub-wide setting needs a design issue.

export type Feature = "automations";

const key = (f: Feature) => `regulus.feature.${f}`;
const CHANGED = "regulus:features";

function read(f: Feature): boolean {
  try {
    return window.localStorage.getItem(key(f)) === "on";
  } catch {
    return false;
  }
}

function subscribe(callback: () => void) {
  window.addEventListener(CHANGED, callback);
  window.addEventListener("storage", callback);
  return () => {
    window.removeEventListener(CHANGED, callback);
    window.removeEventListener("storage", callback);
  };
}

export function useFeature(f: Feature): [boolean, (on: boolean) => void] {
  const on = useSyncExternalStore(
    subscribe,
    () => read(f),
    () => false,
  );
  const set = useCallback(
    (next: boolean) => {
      try {
        window.localStorage.setItem(key(f), next ? "on" : "off");
      } catch {
        // not saved
      }
      window.dispatchEvent(new Event(CHANGED));
    },
    [f],
  );
  return [on, set];
}
