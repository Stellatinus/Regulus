import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

// The colour theme of the dashboard: dark (the design, and the default), light,
// or the system's choice. The choice is saved in this browser. The stylesheet
// reads data-theme on <html>: without it the design's dark theme shows unless
// the system prefers light.
export type Theme = "light" | "dark" | "system";

export const THEMES: Theme[] = ["system", "light", "dark"];

const STORAGE_KEY = "regulus.theme";

// The design of the dashboard is dark.
export const DEFAULT_THEME: Theme = "dark";

const isTheme = (v: unknown): v is Theme => v === "light" || v === "dark" || v === "system";

export function savedTheme(): Theme {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (isTheme(saved)) return saved;
  } catch {
    // no storage
  }
  return DEFAULT_THEME;
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
      window.localStorage.setItem(STORAGE_KEY, next);
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
