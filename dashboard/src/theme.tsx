import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

// The colour theme of the dashboard: light, dark, or the system's choice (the
// default). The choice is saved in this browser; "system" removes it. The
// stylesheet reads data-theme on <html>: without it, prefers-color-scheme
// decides.
export type Theme = "light" | "dark" | "system";

export const THEMES: Theme[] = ["system", "light", "dark"];

const STORAGE_KEY = "regulus.theme";

const isTheme = (v: unknown): v is Theme => v === "light" || v === "dark" || v === "system";

export function savedTheme(): Theme {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (isTheme(saved)) return saved;
  } catch {
    // no storage
  }
  return "system";
}

// applyTheme sets the attribute the stylesheet reads.
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

interface ThemeState {
  theme: Theme;
  setTheme: (theme: Theme) => void;
}

const ThemeContext = createContext<ThemeState | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(savedTheme);
  const setTheme = useCallback((next: Theme) => {
    setThemeState(next);
    try {
      if (next === "system") window.localStorage.removeItem(STORAGE_KEY);
      else window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // not saved; the choice still applies to this page
    }
  }, []);
  useEffect(() => applyTheme(theme), [theme]);
  const value = useMemo(() => ({ theme, setTheme }), [theme, setTheme]);
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeState {
  const ctx = useContext(ThemeContext);
  return ctx ?? { theme: savedTheme(), setTheme: applyTheme };
}
