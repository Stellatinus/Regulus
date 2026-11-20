import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, CODE_REQUIRED, reauthenticate, setReauthHandler } from "./api";
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
  const pending = useRef<{ promise: Promise<boolean>; resolve: (ok: boolean) => void } | null>(null);

  useEffect(() => {
    const remove = setReauthHandler(() => {
      if (pending.current) return pending.current.promise;
      let resolve: (ok: boolean) => void = () => {};
      const promise = new Promise<boolean>((r) => {
        resolve = r;
      });
      pending.current = { promise, resolve };
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
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    reauthenticate(password, askCode ? code.trim() : undefined).then(
      () => finish(true),
      (err: unknown) => {
        setBusy(false);
        setError(err instanceof ApiError || err instanceof Error ? err.message : String(err));
        if (err instanceof ApiError && err.code === CODE_REQUIRED) setRefusedWithoutCode(true);
      },
    );
  };

  if (!open) return null;
  return (
    <div className="modal-backdrop">
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="step-up-heading"
        onKeyDown={(e) => {
          if (e.key === "Escape") finish(false);
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
      </div>
    </div>
  );
}
