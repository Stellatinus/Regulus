import { Component, type ErrorInfo, type ReactNode } from "react";
import { useT } from "./i18n";

// RootBoundary catches an error that escaped every other boundary (a page, the
// shell, the login), so the user sees what happened and a way out instead of
// a white page. Widgets have their own boundary in Dashboard.tsx; this one is
// the last resort around the whole app. It does not try to recover by itself:
// reloading the page is the reliable way back.
export class RootBoundary extends Component<{ children: ReactNode; reload?: () => void }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("the dashboard failed", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return <RootFailed error={this.state.error} reload={this.props.reload ?? (() => window.location.reload())} />;
    }
    return this.props.children;
  }
}

function RootFailed({ error, reload }: { error: Error; reload: () => void }) {
  const t = useT();
  return (
    <main className="root-error" role="alert">
      <h1>{t("app.crashed.title")}</h1>
      <p>{t("app.crashed.body")}</p>
      <button type="button" onClick={reload}>
        {t("app.crashed.reload")}
      </button>
      <details>
        <summary>{t("app.crashed.details")}</summary>
        <pre>{error.message || String(error)}</pre>
      </details>
    </main>
  );
}
