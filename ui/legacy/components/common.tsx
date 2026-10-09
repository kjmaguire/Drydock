import type { ReactNode } from 'react';
import type { PresenceState } from '../api/types';
import { statusIcon, statusLabel, statusTone } from '../lib/format';

export function PresenceDot({ presence }: { presence: PresenceState | undefined }) {
  // shape + text, never color alone (docs/ui.md section 16)
  const name = presence?.presence ?? 'Unknown';
  const cls = name.toLowerCase().replace(/\s+/g, '-');
  const label = presence?.label ? `${name} (${presence.label})` : name;
  return (
    <span className={`presence presence-${cls}`} title={presence?.activity ? `${label}: ${presence.activity}` : label} data-testid="presence">
      <span className="presence-dot" aria-hidden="true" />
      <span className="presence-text">{label}</span>
    </span>
  );
}

export function StatusChip({ status }: { status: string }) {
  return (
    <span className={`chip chip-${statusTone(status)}`} data-testid="task-status">
      <span aria-hidden="true">{statusIcon(status)}</span> {statusLabel(status)}
    </span>
  );
}

/** Where a token or cost number came from (docs/ui.md section 1). `unknown` means the API does not say. */
export function SourceBadge({ source }: { source: 'reported' | 'estimated' | 'unknown' }) {
  const text = source === 'unknown' ? 'source not exposed' : source;
  const hint =
    source === 'unknown'
      ? 'The core sums cost per task but does not return whether it was reported by the CLI or estimated.'
      : source === 'reported'
        ? 'Reported by the agent CLI'
        : 'Estimated by Drydock from a price table';
  return (
    <span className={`badge badge-source-${source}`} title={hint}>
      {text}
    </span>
  );
}

export function ErrorNote({ error, what }: { error: unknown; what?: string }) {
  if (!error) return null;
  const msg = error instanceof Error ? error.message : String(error);
  return (
    <div className="error-note" role="alert">
      {what ? <strong>{what}: </strong> : null}
      {msg}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <h2>{title}</h2>
      {children}
    </div>
  );
}

export function Badge({ children, tone = 'muted', title }: { children: ReactNode; tone?: 'ok' | 'err' | 'warn' | 'info' | 'muted'; title?: string }) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      {children}
    </span>
  );
}

export function Page({ title, actions, children }: { title: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="page" aria-labelledby="page-title">
      <header className="page-head">
        <h1 id="page-title">{title}</h1>
        <div className="page-actions">{actions}</div>
      </header>
      <div className="page-body">{children}</div>
    </section>
  );
}
