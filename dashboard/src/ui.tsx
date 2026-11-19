import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { useT } from "./i18n";

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
