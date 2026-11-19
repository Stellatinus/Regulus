import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { LanguageProvider } from "./i18n";
import { RootBoundary } from "./RootBoundary";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <LanguageProvider>
      <RootBoundary>
        <App />
      </RootBoundary>
    </LanguageProvider>
  </StrictMode>,
);
