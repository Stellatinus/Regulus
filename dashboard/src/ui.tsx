import {
  Component,
  useEffect,
  useState,
  useSyncExternalStore,
  type ErrorInfo,
  type ReactNode,
} from "react";
import { useT } from "./i18n";

// PanelBoundary keeps an error inside one panel of a page: the panel shows
// its own message and the rest of the page stays (#424). RootBoundary is
// only for what no panel could catch.
export class PanelBoundary extends Component<
  { name: string; children: ReactNode },
  { error: Error | null }
> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(`panel "${this.props.name}" failed`, error, info.componentStack);
  }

  render() {
    if (this.state.error) return <PanelFailed message={this.state.error.message} />;
    return this.props.children;
  }
}

function PanelFailed({ message }: { message: string }) {
  const t = useT();
  return <p role="alert">{t("servers.panel.failed", { message })}</p>;
}

// Shared pieces of the web UI (#91): the inline confirmation that replaces
// the browser's confirm(), the narrow-screen row menu and a media query hook.

// useMediaQuery follows a CSS media query. Without matchMedia (tests, old
// browsers) it answers false: the wide layout.
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      if (typeof window.matchMedia !== "function") return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener("change", onChange);
      return () => mql.removeEventListener("change", onChange);
    },
    () => (typeof window.matchMedia === "function" ? window.matchMedia(query).matches : false),
    () => false,
  );
}

// NARROW matches the width where tables become cards (styles.css).
export const NARROW = "(max-width: 48rem)";

interface ConfirmProps {
  // The button that starts the action, and its accessible name.
  label: string;
  // What happens, said before anything happens.
  message: string;
  // The button that does it.
  confirmLabel: string;
  onConfirm: () => void;
  disabled?: boolean;
  danger?: boolean;
  className?: string;
}

// ConfirmButton asks in place before a destructive action: the first click
// shows what will happen with a confirm and a cancel button; focus moves to
// the confirmation, and Escape cancels.
export function ConfirmButton({ label, message, confirmLabel, onConfirm, disabled, danger = true, className }: ConfirmProps) {
  const t = useT();
  const [asking, setAsking] = useState(false);
  const [box, setBox] = useState<HTMLDivElement | null>(null);

  useEffect(() => {
    box?.querySelector<HTMLButtonElement>("button")?.focus();
  }, [box]);

  if (!asking) {
    return (
      <button type="button" className={[danger ? "danger" : "", className ?? ""].join(" ").trim() || undefined} disabled={disabled} onClick={() => setAsking(true)}>
        {label}
      </button>
    );
  }
  return (
    <div
      className="confirm"
      role="group"
      aria-label={label}
      ref={setBox}
      onKeyDown={(e) => {
        if (e.key === "Escape") setAsking(false);
      }}
    >
      <p>{message}</p>
      <button
        type="button"
        className={danger ? "danger solid" : "primary"}
        onClick={() => {
          setAsking(false);
          onConfirm();
        }}
      >
        {confirmLabel}
      </button>
      <button type="button" onClick={() => setAsking(false)}>
        {t("ui.cancel")}
      </button>
    </div>
  );
}

// RowActions shows a table row's actions side by side on wide screens and in
// a menu ("Actions") on narrow ones, where the table becomes cards.
export function RowActions({ name, children }: { name: string; children: ReactNode }) {
  const t = useT();
  const narrow = useMediaQuery(NARROW);
  if (!narrow) return <div className="row-actions">{children}</div>;
  return (
    <details className="row-menu">
      <summary aria-label={t("ui.actionsFor", { name })}>{t("ui.actions")}</summary>
      <div className="row-actions">{children}</div>
    </details>
  );
}

// copyText puts text in the clipboard. The clipboard API exists only on
// secure origins (HTTPS or localhost), and a hub reached over plain HTTP by
// address has none: then it resolves false, so the caller can say how to
// copy by hand, and never throws.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (!window.isSecureContext || !navigator.clipboard) return false;
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// CopyIconButton copies a value from a button that is only an icon: it has an
// accessible name and a tooltip (label), takes a square of its own and never
// grows with its text. It shows a check for a moment and says "Copied" to
// screen readers. It tells the caller the result, so that a failure can be
// written where the caller has room (under the command, #427) instead of
// beside the button.
export function CopyIconButton({
  text,
  label,
  copiedLabel,
  onResult,
}: {
  text: string;
  label: string;
  copiedLabel?: string;
  onResult?: (ok: boolean) => void;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  return (
    <>
      <button
        type="button"
        className="icon-button"
        aria-label={label}
        title={label}
        onClick={() =>
          void copyText(text).then((ok) => {
            setCopied(ok);
            onResult?.(ok);
          })
        }
      >
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          {copied ? (
            <path d="M3 8.5l3.2 3.2L13 4.8" />
          ) : (
            <>
              <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
              <path d="M10.5 5.5V3.5a1.5 1.5 0 0 0-1.5-1.5H3.5A1.5 1.5 0 0 0 2 3.5V9a1.5 1.5 0 0 0 1.5 1.5h2" />
            </>
          )}
        </svg>
      </button>
      <span role="status" className="visually-hidden">
        {copied ? (copiedLabel ?? t("ui.copied")) : ""}
      </span>
    </>
  );
}

// CopyButton copies a value and says "Copied", or, where the browser cannot
// copy, tells the user to select the text and copy it by hand.
export function CopyButton({ text, label, copiedLabel }: { text: string; label: string; copiedLabel?: string }) {
  const t = useT();
  const [state, setState] = useState<"idle" | "copied" | "manual">("idle");
  return (
    <>
      <button type="button" onClick={() => void copyText(text).then((ok) => setState(ok ? "copied" : "manual"))}>
        {state === "copied" ? (copiedLabel ?? t("ui.copied")) : label}
      </button>
      {state === "manual" && (
        <span role="status" className="hint">
          {t("ui.copyManual")}
        </span>
      )}
    </>
  );
}
