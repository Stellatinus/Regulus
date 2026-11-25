import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./fonts.css";
import "@fontsource/jetbrains-mono/latin-400.css";
import "@fontsource/jetbrains-mono/latin-ext-400.css";
import "@fontsource/jetbrains-mono/latin-600.css";
import { App } from "./App";
import { LanguageProvider } from "./i18n";
import { RootBoundary } from "./RootBoundary";
import { ThemeProvider } from "./theme";
import "./styles.css";
import "./shell.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <LanguageProvider>
        <RootBoundary>
          <App />
        </RootBoundary>
      </LanguageProvider>
    </ThemeProvider>
  </StrictMode>,
);
