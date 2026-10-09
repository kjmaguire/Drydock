import { css, type Handle, type RemixNode } from 'remix/component';
import type { PresenceState } from '../api/types.ts';
import { statusLabel, statusTone } from '../lib/format.ts';
import { t, tone, type Tone } from './tokens.ts';

const badgeCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  height: '20px',
  padding: '0 7px',
  borderRadius: t.rFull,
  fontSize: t.textXs,
  fontWeight: '500',
  lineHeight: '1',
  whiteSpace: 'nowrap',
  verticalAlign: 'middle',
});

/** A small pill. Tone sets its color; meaning is always in the text too. */
export function Badge(handle: Handle<{ tone?: Tone; title?: string; children?: RemixNode; testId?: string }>) {
  return () => {
    const { tone: tn = 'muted', title, children, testId } = handle.props;
    const c = tone[tn];
    return (
      <span mix={badgeCss} title={title} data-testid={testId} style={{ color: c.fg, background: c.bg }}>
        {children}
      </span>
    );
  };
}

const dotCss = css({ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', flex: 'none' });

/** A task status: colored dot plus the words (docs/ui.md 16: never color alone). */
export function StatusChip(handle: Handle<{ status: string }>) {
  return () => {
    const s = handle.props.status;
    const c = tone[statusTone(s)];
    return (
      <span mix={badgeCss} data-testid="task-status" style={{ color: c.fg, background: c.bg }}>
        <span mix={dotCss} aria-hidden="true" style={{ background: c.fg }} />
        {statusLabel(s)}
      </span>
    );
  };
}

const PRESENCE_TONE: Record<string, string> = {
  working: t.success,
  thinking: t.info,
  waiting: t.warning,
  needs: t.warning,
  failed: t.danger,
  paused: t.textMuted,
  completed: t.accentText,
  idle: t.textFaint,
};

const presenceCss = css({ display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: t.textSm, color: t.textMuted });

/** Presence: a dot plus the label in words. */
export function PresenceDot(handle: Handle<{ presence: PresenceState | undefined; compact?: boolean }>) {
  return () => {
    const p = handle.props.presence;
    const name = p?.presence ?? 'Unknown';
    const label = p?.label ? `${name} (${p.label})` : name;
    const color = PRESENCE_TONE[name.toLowerCase().split(/\s/)[0] ?? ''] ?? t.textFaint;
    return (
      <span mix={presenceCss} title={p?.activity ? `${label}: ${p.activity}` : label} data-testid="presence">
        <span mix={dotCss} aria-hidden="true" style={{ background: color }} />
        <span class={handle.props.compact ? 'sr-only' : undefined}>{label}</span>
      </span>
    );
  };
}

/** The presence color for an avatar's status ring. */
export function presenceColor(p: PresenceState | undefined): string {
  return PRESENCE_TONE[(p?.presence ?? '').toLowerCase().split(/\s/)[0] ?? ''] ?? t.textFaint;
}

// Avatar colors: chosen by a stable hash of the id so an agent keeps its color everywhere.
const HUES = [234, 262, 199, 160, 32, 340, 12, 288, 180, 48];
export function hueFor(id: string): number {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return HUES[h % HUES.length]!;
}

export function initials(name: string): string {
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[1]![0]!).toUpperCase();
}

const avatarCss = css({
  position: 'relative',
  display: 'inline-grid',
  placeItems: 'center',
  flex: 'none',
  borderRadius: '50%',
  fontWeight: '600',
  color: '#fff',
  userSelect: 'none',
  letterSpacing: '0.02em',
});

const avatarStatus = css({
  position: 'absolute',
  right: '-2px',
  bottom: '-2px',
  width: '10px',
  height: '10px',
  borderRadius: '50%',
  border: `2px solid ${t.bgApp}`,
});

/** A round avatar with initials; `square` gives the rounded-square project look. */
export function Avatar(handle: Handle<{ id: string; name: string; size?: number; square?: boolean; status?: string; kind?: 'agent' | 'human' | 'system' }>) {
  return () => {
    const { id, name, size = 32, square, status, kind } = handle.props;
    const hue = hueFor(id);
    const bg = kind === 'system' ? t.bgActive : kind === 'human' ? `hsl(${hue} 30% 42%)` : `linear-gradient(135deg, hsl(${hue} 70% 58%), hsl(${(hue + 30) % 360} 65% 46%))`;
    return (
      <span
        mix={avatarCss}
        aria-hidden="true"
        style={{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.38)}px`, background: bg, borderRadius: square ? `${Math.round(size * 0.3)}px` : '50%', color: kind === 'system' ? t.textMuted : '#fff' }}
      >
        {initials(name)}
        {status ? <span mix={avatarStatus} style={{ background: status }} /> : null}
      </span>
    );
  };
}

/** Where a token or cost number came from (docs/ui.md section 1). `unknown` means the API does not say. */
export function SourceBadge(handle: Handle<{ source: 'reported' | 'estimated' | 'unknown' }>) {
  return () => {
    const source = handle.props.source;
    const text = source === 'unknown' ? 'source not exposed' : source;
    const hint =
      source === 'unknown'
        ? 'The core sums cost per task but does not return whether it was reported by the CLI or estimated.'
        : source === 'reported'
          ? 'Reported by the agent CLI'
          : 'Estimated by Drydock from a price table';
    return (
      <Badge tone={source === 'reported' ? 'ok' : source === 'estimated' ? 'warn' : 'muted'} title={hint}>
        {text}
      </Badge>
    );
  };
}

/** A definition list laid out as two aligned columns. */
export const kv = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(110px, max-content) 1fr',
  gap: '6px 16px',
  margin: '0',
  fontSize: t.textMd,
  '& dt': { color: t.textMuted },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
});

/** Muted helper text. */
export const muted = css({ color: t.textMuted });
export const faint = css({ color: t.textFaint, fontSize: t.textSm });
export const mono = css({ fontFamily: t.mono, fontSize: t.textSm });
