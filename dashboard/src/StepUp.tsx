import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import {
  ApiError,
  CODE_REQUIRED,
  reauthenticate,
  setReauthHandler,
} from "./api";
import { useT } from "./i18n";

// StepUp is the dialog of step-up re-authentication (#279). Sensitive actions
// (users, roles, enrollment tokens, updates...) need a recent proof of the
// password; when the hub answers reauth_required, the API client calls the
// handler registered here, which opens the dialog. On success the client
// repeats the request; Cancel makes it fail with the hub's message. Several
// requests refused together share one dialog.
export function StepUp({ twoFactor }: { twoFactor: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  // The code field shows when the user has two-factor on, or once the hub
  // asked for one (two-factor turned on since the page loaded).
  const [refusedWithoutCode, setRefusedWithoutCode] = useState(false);
  const askCode = twoFactor || refusedWithoutCode;
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef<{
    promise: Promise<boolean>;
    resolve: (ok: boolean) => void;
  } | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  // The element that had the focus when the dialog opened gets it back.
  const opener = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const remove = setReauthHandler(() => {
      if (pending.current) return pending.current.promise;
      let resolve: (ok: boolean) => void = () => {};
      const promise = new Promise<boolean>((r) => {
        resolve = r;
      });
      pending.current = { promise, resolve };
      opener.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      setOpen(true);
      return promise;
    });
    return () => {
      remove();
      pending.current?.resolve(false);
      pending.current = null;
    };
  }, []);

  const finish = (ok: boolean) => {
    pending.current?.resolve(ok);
    pending.current = null;
    setOpen(false);
    setPassword("");
    setCode("");
    setError(null);
    setBusy(false);
    const back = opener.current;
    opener.current = null;
    if (back?.isConnected) back.focus();
  };

  // Tab and Shift+Tab stay inside the dialog; Escape closes it.
  const keys = (e: KeyboardEvent<HTMLDialogElement>) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      finish(false);
      return;
    }
    if (e.key !== "Tab" || !dialog.current) return;
    const items = Array.from(
      dialog.current.querySelectorAll<HTMLElement>("input, button"),
    ).filter((el) => !(el as HTMLInputElement).disabled);
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    reauthenticate(password, askCode ? code.trim() : undefined).then(
      () => finish(true),
      (err: unknown) => {
        setBusy(false);
        setError(
          err instanceof ApiError || err instanceof Error
            ? err.message
            : String(err),
        );
        if (err instanceof ApiError && err.code === CODE_REQUIRED)
          setRefusedWithoutCode(true);
      },
    );
  };

  // A native modal dialog goes in the browser's top layer, above every other
  // element whatever its z-index, and above a modal dialog opened earlier (the
  // store's detail window, for one): a div with a z-index of its own was
  // covered by it, and the password could not be typed.
  useEffect(() => {
    const d = dialog.current;
    if (!open || !d || d.open) return;
    if (typeof d.showModal === "function") d.showModal();
    else d.setAttribute("open", ""); // no top layer to use (an old browser, jsdom)
    d.querySelector<HTMLInputElement>("input")?.focus();
  }, [open]);

  if (!open) return null;
  return (
    <dialog
      className="modal"
      aria-labelledby="step-up-heading"
      ref={dialog}
      onKeyDown={keys}
      onCancel={(e) => {
        e.preventDefault(); // Escape: finish() closes it
        finish(false);
      }}
    >
      <form onSubmit={submit}>
        <h2 id="step-up-heading">{t("auth.stepUp.title")}</h2>
        <p>{t("auth.stepUp.intro")}</p>
        <label>
          {t("auth.stepUp.password")}
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            autoFocus
            required
          />
        </label>
        {askCode && (
          <>
            <label>
              {t("auth.stepUp.code")}
              <input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="one-time-code"
                required
              />
            </label>
            <p className="hint">{t("auth.stepUp.codeHint")}</p>
          </>
        )}
        {error && <p role="alert">{error}</p>}
        <p className="row-actions">
          <button type="submit" className="primary" disabled={busy}>
            {t("auth.stepUp.confirm")}
          </button>
          <button type="button" onClick={() => finish(false)}>
            {t("auth.stepUp.cancel")}
          </button>
        </p>
      </form>
    </dialog>
  );
}
