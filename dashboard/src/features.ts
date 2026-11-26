import { useCallback, useSyncExternalStore } from "react";

// The features an administrator can switch on or off: Plugins, Apps and
// Automations. Plugins exist on the hub and start on; Apps and Automations do
// not exist yet (they will have their own stores, #475) and start off. The
// dock shows the Services section when Plugins or Apps are on, and
// Automations only while their switch is on. For now the choice is kept in
// this browser; a setting for the whole hub needs a design (#474).

export type Feature = "plugins" | "apps" | "automations";

export const FEATURES: readonly Feature[] = ["plugins", "apps", "automations"];

const DEFAULTS: Record<Feature, boolean> = { plugins: true, apps: false, automations: false };

const key = (f: Feature) => `regulus.feature.${f}`;
const CHANGED = "regulus:features";

function read(f: Feature): boolean {
  try {
    const v = window.localStorage.getItem(key(f));
    if (v === "on") return true;
    if (v === "off") return false;
  } catch {
    // no storage
  }
  return DEFAULTS[f];
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
    () => DEFAULTS[f],
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
