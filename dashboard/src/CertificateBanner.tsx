import { useEffect, useState } from "react";
import { fetchCertificates, type CertificateEnd, type Certificates } from "./api";
import { useT } from "./i18n";
import { useCan } from "./session";

const REFRESH_MS = 10 * 60 * 1000;

// daysLeft is how many whole days are left before a date (negative once it
// has passed).
export function daysLeft(notAfter: string, now = Date.now()): number {
  return Math.floor((Date.parse(notAfter) - now) / 86_400_000);
}

// CertificateBanner warns those who manage servers when the hub's CA or an
// agent's certificate ends within 90 days (#245), with the way to renew it.
// Nothing is shown to others, or when nothing ends soon.
export function CertificateBanner() {
  const canManage = useCan("servers.manage");
  const [certs, setCerts] = useState<Certificates | null>(null);
  useEffect(() => {
    if (!canManage) return;
    const controller = new AbortController();
    const load = () => fetchCertificates(controller.signal).then(setCerts, () => undefined);
    void load();
    const timer = setInterval(() => void load(), REFRESH_MS);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [canManage]);
  const t = useT();
  if (!canManage || !certs || !Array.isArray(certs.warnings) || certs.warnings.length === 0) return null;
  const hasCA = certs.warnings.some((w) => w.kind === "ca");
  const hasAgent = certs.warnings.some((w) => w.kind === "agent");
  return (
    <div className="notice warn" role="status" aria-labelledby="cert-warning-title">
      <p>
        <strong id="cert-warning-title">{t("servers.certs.title")}</strong>
      </p>
      <ul>
        {certs.warnings.map((w) => (
          <li key={`${w.kind}-${w.agent_id ?? ""}`}>{describe(w, t)}</li>
        ))}
      </ul>
      {hasAgent && <p>{t("servers.certs.renewAgent")}</p>}
      {hasCA && <p>{t("servers.certs.renewCA")}</p>}
    </div>
  );
}

function describe(w: CertificateEnd, t: ReturnType<typeof useT>): string {
  const what =
    w.kind === "ca"
      ? t("servers.certs.ca")
      : w.kind === "listener"
        ? t("servers.certs.listener")
        : t("servers.certs.agent", { name: w.name || w.agent_id || "?" });
  const date = new Date(w.not_after).toLocaleDateString();
  return w.days_left < 0 ? t("servers.certs.ended", { what, date }) : t("servers.certs.ends", { what, date, days: w.days_left });
}
