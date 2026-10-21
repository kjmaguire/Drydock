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

const eyesCss = css({
  position: 'absolute',
  inset: '0',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  paddingBottom: '8%',
  pointerEvents: 'none',
  '& > i': { display: 'block', background: 'currentColor' },
});

/** The mood an agent's eyes show, from its presence color (the words sit beside the avatar). */
export function moodFor(status: string | undefined): string {
  if (!status) return 'idle';
  for (const [k, v] of Object.entries(STATUS_TONE)) if (v === status) return STATUS_MOOD[k as AgentStatus];
  return 'idle';
}

/** The eyes (app.css .dot-eyes): looking around at work, up while waiting on you, closed while stopped. */
const STATUS_MOOD: Record<AgentStatus, string> = {
  working: 'working',
  'waiting-for-you': 'waiting',
  waiting: 'thinking',
  starting: 'thinking',
  idle: 'idle',
  error: 'failed',
  stopped: 'paused',
};

/**
 * An avatar. Agents are "dots" (OpenAI Dots, Grok Bot): a disc in the agent's own bright colour with two rounded-square
 * eyes that blink, look around while the agent works, look up while it waits on you and close while it is paused.
 * People are round with initials; projects and tools are rounded squares with initials. `status` is a presence color.
 */
export function Avatar(handle: Handle<{ id: string; name: string; size?: number; square?: boolean; status?: string; kind?: 'agent' | 'human' | 'system' }>) {
  return () => {
    const { id, name, size = 32, status, kind } = handle.props;
    const hue = hueFor(id);
    const neutral = kind === 'system';
    if (kind === 'agent') {
      const ew = Math.max(2, Math.round(size * 0.15));
      const eh = Math.max(3, Math.round(size * 0.24));
      return (
        <span
          mix={avatarCss}
          aria-hidden="true"
          data-kind={kind}
          data-dot="true"
          style={{
            width: `${size}px`,
            height: `${size}px`,
            borderRadius: '50%',
            background: `radial-gradient(circle at 32% 26%, hsl(${hue} 95% 78%), hsl(${hue} 78% 58%) 62%, hsl(${hue} 70% 48%))`,
            color: '#0b0c0e',
            boxShadow: `inset 0 -${Math.max(1, Math.round(size / 14))}px 0 rgb(0 0 0 / 0.14), 0 0 0 1px rgb(0 0 0 / 0.25)`,
          }}
        >
          <span mix={eyesCss} class="dot-eyes" data-mood={moodFor(status)} style={{ gap: `${Math.max(2, Math.round(size * 0.13))}px`, ['--blink-delay' as string]: `${(hue % 7) * 0.6}s` }}>
            <i style={{ width: `${ew}px`, height: `${eh}px`, borderRadius: `${Math.max(1, Math.round(ew * 0.38))}px` }} />
            <i style={{ width: `${ew}px`, height: `${eh}px`, borderRadius: `${Math.max(1, Math.round(ew * 0.38))}px` }} />
          </span>
          {status && size >= 20 ? <span mix={avatarStatus} style={{ background: status }} /> : null}
        </span>
      );
    }
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
