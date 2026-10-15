import { css, type Handle, type RemixNode } from 'remix/component';
import type { PresenceState } from '../api/types.ts';
import { statusLabel, statusTone } from '../lib/format.ts';
import { Bot } from 'lucide';
import { Icon } from './icon.tsx';
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
      <span mix={badgeCss} title={title} data-testid={testId} data-tone={tn} style={{ color: c.fg, background: c.bg }}>
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
  fontWeight: '600',
  userSelect: 'none',
  letterSpacing: '0.01em',
  lineHeight: '1',
});

const avatarStatus = css({
  position: 'absolute',
  right: '-2px',
  bottom: '-2px',
  width: '9px',
  height: '9px',
  borderRadius: '50%',
  border: `2px solid ${t.bgApp}`,
});

const avatarBot = css({
  position: 'absolute',
  right: '-3px',
  bottom: '-3px',
  display: 'grid',
  placeItems: 'center',
  width: '13px',
  height: '13px',
  borderRadius: '4px',
  background: t.bgRaised,
  color: t.textMuted,
  boxShadow: `0 0 0 1.5px ${t.bgApp}, inset 0 0 0 1px ${t.border}`,
});

/** The corner radius of an avatar of this size: a rounded-md square (6px at 24-28px). */
export function avatarRadius(size: number): number {
  return size <= 16 ? 3 : size <= 20 ? 4 : size <= 30 ? 6 : 8;
}

/**
 * A calm avatar: initials on a neutral surface lightly tinted by a stable per-id hue (low chroma, both themes).
 * People are round; agents, projects and tools are rounded squares, and an agent at 24px or more carries a small bot
 * badge (unless a presence dot takes that corner). `status` is a presence color for the dot.
 */
export function Avatar(handle: Handle<{ id: string; name: string; size?: number; square?: boolean; status?: string; kind?: 'agent' | 'human' | 'system' }>) {
  return () => {
    const { id, name, size = 32, status, kind } = handle.props;
    const hue = hueFor(id);
    const neutral = kind === 'system';
    const bg = neutral ? t.bgActive : `color-mix(in oklab, hsl(${hue} 42% 52%) 16%, ${t.bgRaised})`;
    const fg = neutral ? t.textMuted : `color-mix(in oklab, hsl(${hue} 48% 50%) 55%, ${t.text})`;
    const round = kind === 'human';
    return (
      <span
        mix={avatarCss}
        aria-hidden="true"
        data-kind={kind}
        style={{
          width: `${size}px`,
          height: `${size}px`,
          // never under 10 px: initials at 8-9 px (the 16-18 px avatars of lists) could not be read
          fontSize: `${Math.max(10, Math.round(size * 0.4))}px`,
          background: bg,
          color: fg,
          borderRadius: round ? '50%' : `${avatarRadius(size)}px`,
          boxShadow: `inset 0 0 0 1px ${t.border}`,
        }}
      >
        {size < 22 ? initials(name).slice(0, 1) : initials(name)}
        {status ? <span mix={avatarStatus} style={{ background: status }} /> : kind === 'agent' && size >= 24 ? <span mix={avatarBot} class="avatar-bot"><Icon icon={Bot} size={9} strokeWidth={2.25} /></span> : null}
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
