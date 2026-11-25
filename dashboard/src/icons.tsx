import type { ReactNode } from "react";

// Icons are drawn inline (no icon library): 20-unit strokes in currentColor.
const icon = (d: ReactNode, size = 16) => (
  <svg width={size} height={size} viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    {d}
  </svg>
);

export const icons = {
  dashboard: icon(
    <>
      <rect x="3" y="3" width="6" height="8" rx="1.4" />
      <rect x="11" y="3" width="6" height="4.5" rx="1.4" />
      <rect x="11" y="10" width="6" height="7" rx="1.4" />
      <rect x="3" y="13.5" width="6" height="3.5" rx="1.4" />
    </>,
  ),
  nodes: icon(
    <>
      <rect x="3" y="3.5" width="14" height="5" rx="1.4" />
      <rect x="3" y="11.5" width="14" height="5" rx="1.4" />
      <circle cx="6.2" cy="6" r="0.85" fill="currentColor" />
      <circle cx="6.2" cy="14" r="0.85" fill="currentColor" />
    </>,
  ),
  services: icon(
    <>
      <path d="M10 3L16.5 6.75V13.25L10 17L3.5 13.25V6.75L10 3Z" />
      <path d="M3.5 6.75L10 10.5L16.5 6.75M10 10.5V17" strokeOpacity="0.65" />
    </>,
  ),
  automations: icon(<path d="M11 2.8L4.8 11h4.7l-.5 6.2L15.2 9h-4.7l.5-6.2Z" />),
  admin: icon(
    <>
      <path d="M10 2.8L16 5.3V10.1C16 13.9 13.4 16.6 10 17.8C6.6 16.6 4 13.9 4 10.1V5.3L10 2.8Z" />
      <circle cx="10" cy="9.5" r="1.4" />
      <path d="M10 11v2" />
    </>,
  ),
  // The settings cog: a gear with teeth (not a sun).
  settings: icon(
    <>
      <circle cx="10" cy="10" r="2.4" />
      <path d="M8.6 2.8h2.8l.4 1.9 1.2.5 1.6-1.1 2 2-1.1 1.6.5 1.2 1.9.4v2.8l-1.9.4-.5 1.2 1.1 1.6-2 2-1.6-1.1-1.2.5-.4 1.9H8.6l-.4-1.9-1.2-.5-1.6 1.1-2-2 1.1-1.6-.5-1.2-1.9-.4V8.6l1.9-.4.5-1.2L3.4 5.4l2-2L7 4.5l1.2-.5.4-1.9Z" />
    </>,
  ),
  chevron: icon(<path d="M4.5 6.5L8 10l3.5-3.5" />, 14),
  logout: icon(<path d="M7.5 16.5H4.8A1.8 1.8 0 0 1 3 14.7V5.3a1.8 1.8 0 0 1 1.8-1.8h2.7M13 13.5L16.5 10 13 6.5M16.5 10H8" />),
  user: icon(
    <>
      <circle cx="10" cy="7" r="3" />
      <path d="M4 16.5c.7-3 3-4.5 6-4.5s5.3 1.5 6 4.5" />
    </>,
  ),
};
