import { createContext, useContext, useMemo, type ReactNode } from 'react';
import type { LicenseView } from '../api/pkgTypes';
import { useClient } from './context';
import { useLive } from './hooks';

export interface LicenseInfo {
  view: LicenseView | undefined;
  /** True only when licensing is configured in this build and the state is none or expired. A build without keys is never read-only. */
  readOnly: boolean;
  /** The core's own words for why work is turned off; empty when nothing is. */
  reason: string;
  /** Re-reads license.status now (after an import or clear). */
  reload: () => void;
}

const Ctx = createContext<LicenseInfo | null>(null);

export function licenseInfo(view: LicenseView | undefined, reload: () => void = () => {}): LicenseInfo {
  const readOnly = !!view && view.configured && view.readOnly;
  return { view, readOnly, reason: readOnly ? (view?.banner || view?.message || 'Drydock is read-only.') : '', reload };
}

/** Reads license.status and re-reads on license events, so controls can be disabled before the core refuses them. */
export function LicenseProvider({ children }: { children: ReactNode }) {
  const client = useClient();
  const q = useLive(() => client.licenseStatus(), [], (e) => e.type.startsWith('license.'));
  const value = useMemo(() => licenseInfo(q.data, q.reload), [q.data, q.reload]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

const none: LicenseInfo = { view: undefined, readOnly: false, reason: '', reload: () => {} };

/** Components used without a provider (unit tests) are never read-only. */
export function useLicense(): LicenseInfo {
  return useContext(Ctx) ?? none;
}

export function useReadOnly(): { readOnly: boolean; reason: string } {
  const l = useLicense();
  return { readOnly: l.readOnly, reason: l.reason };
}

/** Shown next to controls that are turned off while read-only, with the core's reason verbatim. */
export function ReadOnlyNote() {
  const { readOnly, reason } = useReadOnly();
  if (!readOnly) return null;
  return (
    <p className="readonly-note" role="note" data-testid="readonly-note">
      <strong>Read-only.</strong> {reason} <a href="#/license">Open License</a>
    </p>
  );
}

/** The persistent banner: loud and plain when licensing is not configured, a warning in grace, an alert when read-only. */
export function LicenseBanner() {
  const { view, readOnly } = useLicense();
  if (!view || !view.banner) return null;
  const tone = readOnly ? 'err' : view.state === 'grace' ? 'warn' : !view.configured ? 'info' : 'warn';
  return (
    <div className={`license-banner banner-${tone}`} role={readOnly ? 'alert' : 'status'} data-testid="license-banner" data-state={view.state} data-configured={view.configured}>
      <strong>{!view.configured ? 'Licensing is not configured. ' : ''}</strong>
      <span>{view.banner}</span> <a href="#/license">License</a>
    </div>
  );
}
