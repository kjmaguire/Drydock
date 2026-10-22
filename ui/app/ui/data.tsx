import { css, type Handle, type RemixNode } from 'remix/component';
import type { PresenceState } from '../api/types.ts';
import { STATUS_WORDS, agentStatus, statusLabel, statusTone, type AgentStatus } from '../lib/format.ts';
import { LEAD_ORIGIN_LABEL, leadStep, leadStepText } from '../lib/task-origin.ts';
import { t, tone, type Tone } from './tokens.ts';

const badgeCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  height: '20px',
  padding: '0 7px',
  borderRadius: t.rFull,
  fontSize: t.textXs,
  fontWeight: '600',
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

/**
 * Where a task came from, when it is not a person's: "Split off by the team lead" for an origin "lead:N" (the tooltip says how
 * many steps it is from a task the person made). Takes whatever the core sent as `origin`: any other value, or none, draws nothing.
 */
export function OriginBadge(handle: Handle<{ origin: unknown }>) {
  return () => {
    const step = leadStep(handle.props.origin);
    if (step === undefined) return null;
    return (
      <Badge tone="info" title={leadStepText(step)} testId="task-origin">
        {LEAD_ORIGIN_LABEL}
      </Badge>
    );
  };
}

const STATUS_TONE: Record<AgentStatus, string> = {
  working: t.success,
  'waiting-for-you': t.warning,
  waiting: t.info,
  starting: t.info,
  idle: t.textFaint,
  error: t.danger,
  stopped: t.textMuted,
};

const presenceCss = css({ display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: t.textSm, color: t.textMuted });

/** Presence: a dot plus the label in words. */
export function PresenceDot(handle: Handle<{ presence: PresenceState | undefined; compact?: boolean }>) {
  return () => {
    const p = handle.props.presence;
    const status = agentStatus(p);
    const label = STATUS_WORDS[status];
    const color = STATUS_TONE[status];
    return (
      <span mix={presenceCss} title={p?.activity ? `${label}: ${p.activity}` : label} data-testid="presence" data-status={status} data-presence={p?.presence}>
        <span mix={dotCss} aria-hidden="true" data-pulse={status === 'working' ? 'true' : undefined} style={{ background: color, color }} />
        <span class={handle.props.compact ? 'sr-only' : undefined}>{label}</span>
      </span>
    );
  };
}

/** The presence color for an avatar's status ring. */
export function presenceColor(p: PresenceState | undefined): string {
  return STATUS_TONE[agentStatus(p)];
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

/** The corner radius of an avatar of this size: a rounded-md square (6px at 24-28px). */
export function avatarRadius(size: number): number {
  return size <= 16 ? 3 : size <= 20 ? 4 : size <= 30 ? 6 : 8;
}

/**
 * An avatar (graphite, decision P97). An agent is a disc in its own hue, deep in the dark theme and pale in the light one,
 * with its first letter; `data-dot` marks it as an agent's (the tests and the e2e suite find agents by it). People are round
 * with initials; projects and tools are rounded squares with initials. `status` is a presence colour, drawn as a corner dot;
 * presence is always in words beside the avatar too.
 */
export function Avatar(handle: Handle<{ id: string; name: string; size?: number; square?: boolean; status?: string; kind?: 'agent' | 'human' | 'system' }>) {
  return () => {
    const { id, name, size = 32, status, kind } = handle.props;
    const hue = hueFor(id);
    const neutral = kind === 'system';
    const agent = kind === 'agent';
    const bg = neutral ? t.bgActive : `light-dark(hsl(${hue} 70% 93%), hsl(${hue} 34% ${agent ? 25 : 21}%))`;
    const fg = neutral ? t.textMuted : `light-dark(hsl(${hue} 50% 30%), hsl(${hue} 90% 86%))`;
    const round = kind === 'human' || agent;
    const letters = agent || size < 22 ? (name.trim()[0] ?? '?').toUpperCase() : initials(name);
    return (
      <span
        mix={avatarCss}
        aria-hidden="true"
        data-kind={kind}
        data-dot={agent ? 'true' : undefined}
        style={{
          width: `${size}px`,
          height: `${size}px`,
          // never under 10 px: initials at 8-9 px (the 16-18 px avatars of lists) could not be read
          fontSize: `${Math.max(10, Math.round(size * (agent ? 0.44 : 0.4)))}px`,
          background: bg,
          color: fg,
          borderRadius: round ? '50%' : `${avatarRadius(size)}px`,
        }}
      >
        {letters}
        {status && size >= 20 ? <span mix={avatarStatus} style={{ background: status }} /> : null}
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
