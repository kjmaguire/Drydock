// The persistent license banner: loud and plain when licensing is not configured, a warning in grace, an alert when
// read-only. Also ReadOnlyNote, shown next to controls that are turned off while read-only.

import { css, type Handle } from 'remix/component';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { t, tone } from '../ui/tokens.ts';

const bannerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  minHeight: '32px',
  padding: `4px ${t.s4}`,
  fontSize: t.textSm,
  borderBottom: `1px solid ${t.border}`,
  '& a': { fontWeight: '600', color: 'inherit', textDecoration: 'underline' },
});

export function LicenseBanner(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  return () => {
    const { view, readOnly } = s.workspace.license;
    if (!view || !view.banner) return null;
    const tn = readOnly ? 'err' : view.state === 'grace' ? 'warn' : !view.configured ? 'info' : 'warn';
    const c = tone[tn];
    return (
      <div
        mix={bannerCss}
        style={{ background: c.bg, color: t.text }}
        role={readOnly ? 'alert' : 'status'}
        data-testid="license-banner"
        data-state={view.state}
        data-configured={String(view.configured)}
      >
        <strong style={{ color: c.fg }}>{!view.configured ? 'Licensing is not configured. ' : ''}</strong>
        <span>{view.banner}</span> <a href={routes.license.href()}>License</a>
      </div>
    );
  };
}

const noteCss = css({
  margin: '0',
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.warningSoft,
  fontSize: t.textMd,
  '& a': { fontWeight: '600' },
});

/** Shown next to controls that are turned off while read-only, with the core's reason verbatim. */
export function ReadOnlyNote(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  return () => {
    const { readOnly, reason } = s.workspace.license;
    if (!readOnly) return null;
    return (
      <p mix={noteCss} role="note" data-testid="readonly-note">
        <strong>Read-only.</strong> {reason} <a href={routes.license.href()}>Open License</a>
      </p>
    );
  };
}
