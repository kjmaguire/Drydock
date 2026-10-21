// A tool call in a chat as a card (decision P84): how it is going (running, done, failed, or no result reported), a readable
// name, the call in one line and how long it took; opened, the call is shown the way a person reads that kind of tool (an
// edit as a diff, a shell command with its output, a read with what it returned) and anything else as its input and result.
// Everything shown is text the core stored (redacted, bounded); nothing is rendered as HTML.

import { css, type Handle } from 'remix/component';
import { Check, ChevronRight, Minus, X } from 'lucide';
import type { ChatMessage } from '../../api/chatClient.ts';
import { Spinner } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { toolInputText } from './chats.ts';

export type ToolState = 'running' | 'ok' | 'failed' | 'unknown';

/** How a tool call is going: its stored status, else running while its turn runs, else no result was reported. */
export function toolState(m: Pick<ChatMessage, 'toolStatus'>, running: boolean): ToolState {
  if (m.toolStatus === 'ok') return 'ok';
  if (m.toolStatus === 'failed') return 'failed';
  return running ? 'running' : 'unknown';
}

/**
 * A tool's name as a person reads it. MCP tools are mcp__<server>__<tool>; a server reached through Drydock's gateway is
 * mcp__drydock__<server>__<tool>, and Drydock's own tools are mcp__drydock__<tool>.
 */
export function toolLabel(name: string | undefined): string {
  const n = name ?? '';
  if (!n.startsWith('mcp__')) return n || 'Tool';
  const parts = n.slice(5).split('__').filter(Boolean);
  if (parts[0] === 'drydock') {
    if (parts.length >= 3) return `${parts[1]} · ${parts.slice(2).join('__')}`;
    if (parts.length === 2) return `Drydock · ${parts[1]}`;
  }
  if (parts.length >= 2) return `${parts[0]} · ${parts.slice(1).join('__')}`;
  return n;
}

/** 840 ms, 1.2 s, 2 min 5 s; '' when unknown. */
export function durationText(ms: number | undefined): string {
  if (!ms || ms <= 0) return '';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return s ? `${m} min ${s} s` : `${m} min`;
}

type Field = Record<string, unknown>;
const fields = (input: unknown): Field => (input && typeof input === 'object' && !Array.isArray(input) ? (input as Field) : {});
const str = (f: Field, k: string): string => (typeof f[k] === 'string' ? (f[k] as string) : '');

/** One line of a diff: '-' removed, '+' added. */
export interface DiffLine {
  kind: '-' | '+';
  text: string;
}

/** The change an edit tool makes, as removed and added lines (Claude's Edit and MultiEdit; ACP edits that carry old and new text). */
export function editLines(input: unknown): DiffLine[] | undefined {
  const f = fields(input);
  const pairs: Array<[string, string]> = [];
  if (Array.isArray(f.edits)) {
    for (const e of f.edits) {
      const ef = fields(e);
      pairs.push([str(ef, 'old_string'), str(ef, 'new_string')]);
    }
  } else if ('old_string' in f || 'new_string' in f) {
    pairs.push([str(f, 'old_string'), str(f, 'new_string')]);
  } else if ('oldText' in f || 'newText' in f) {
    pairs.push([str(f, 'oldText'), str(f, 'newText')]);
  } else {
    return undefined;
  }
  const out: DiffLine[] = [];
  for (const [o, n] of pairs) {
    if (o) for (const l of o.split('\n')) out.push({ kind: '-', text: l });
    if (n) for (const l of n.split('\n')) out.push({ kind: '+', text: l });
  }
  return out;
}

const cardCss = css({
  maxWidth: '760px',
  margin: '2px 0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textSm,
  '&[data-state="failed"]': { borderColor: `color-mix(in srgb, ${t.danger} 40%, ${t.border})` },
  '& > summary': {
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
    minWidth: '0',
    minHeight: '28px',
    padding: `0 ${t.s2}`,
    listStyle: 'none',
    cursor: 'pointer',
    borderRadius: t.rMd,
    color: t.textMuted,
  },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary:hover': { color: t.text, background: t.bgHover },
  '& > summary:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > summary .chev': { display: 'inline-flex', transition: `transform ${t.fast} ${t.ease}` },
  '&[open] > summary .chev': { transform: 'rotate(90deg)' },
  '& > summary .state': { display: 'inline-grid', placeItems: 'center', width: '16px', height: '16px', flex: 'none', borderRadius: '50%' },
  '& > summary .state[data-state="ok"]': { color: t.success },
  '& > summary .state[data-state="failed"]': { color: t.danger },
  '& > summary .state[data-state="unknown"]': { color: t.textFaint },
  '& > summary strong': { color: t.text, fontWeight: '600', flex: 'none', maxWidth: '45%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > summary .sum': { flex: '1 1 auto', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: t.mono, fontSize: t.textXs },
  '& > summary .ms': { flex: 'none', fontSize: t.textXs, color: t.textFaint, fontVariantNumeric: 'tabular-nums' },
  '& .body': { display: 'grid', gap: t.s2, padding: `0 ${t.s2} ${t.s2}` },
  '& .label': { fontSize: t.textXs, color: t.textFaint, fontWeight: '600', textTransform: 'uppercase', letterSpacing: '0.03em' },
  '& pre': { margin: '0', maxHeight: '300px', overflow: 'auto', fontSize: t.textXs, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& .diff': { margin: '0', maxHeight: '300px', overflow: 'auto', fontFamily: t.mono, fontSize: t.textXs, borderRadius: t.rSm, border: `1px solid ${t.border}` },
  '& .diff > div': { padding: '0 6px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& .diff > div[data-kind="-"]': { background: `color-mix(in srgb, ${t.dangerSoft} 80%, transparent)` },
  '& .diff > div[data-kind="+"]': { background: `color-mix(in srgb, ${t.successSoft} 80%, transparent)` },
  '& .cmd': { fontFamily: t.mono, fontSize: t.textXs, color: t.text, overflowWrap: 'anywhere' },
  '& .none': { margin: '0', color: t.textFaint },
});

const STATE_TEXT: Record<ToolState, string> = { running: 'running', ok: 'done', failed: 'failed', unknown: 'no result reported' };

function StateIcon(handle: Handle<{ state: ToolState }>) {
  return () => {
    const s = handle.props.state;
    return (
      <span class="state" data-state={s} title={STATE_TEXT[s]}>
        {s === 'running' ? <Spinner label="Running" /> : <Icon icon={s === 'ok' ? Check : s === 'failed' ? X : Minus} size={13} />}
      </span>
    );
  };
}

export function ToolCard(handle: Handle<{ m: ChatMessage; summary: string; running: boolean }>) {
  return () => {
    const { m, summary, running } = handle.props;
    const state = toolState(m, running);
    const label = toolLabel(m.toolName);
    const ms = durationText(m.toolMs);
    const f = fields(m.toolInput);
    const diff = editLines(m.toolInput);
    const command = str(f, 'command');
    const result = m.toolResult ?? '';
    return (
      <details mix={cardCss} data-testid="chat-tool" data-state={state} aria-label={`${label}: ${STATE_TEXT[state]}`}>
        <summary>
          <span class="chev" aria-hidden="true">
            <Icon icon={ChevronRight} size={12} />
          </span>
          <StateIcon state={state} />
          <strong title={m.toolName}>{label}</strong>
          <span class="sum">{summary}</span>
          {ms ? (
            <span class="ms" data-testid="chat-tool-ms">
              {ms}
            </span>
          ) : null}
        </summary>
        <div class="body">
          {diff ? (
            <div class="diff" role="group" aria-label={`Change made by ${label}`} data-testid="chat-tool-diff">
              {diff.map((l, i) => (
                <div key={i} data-kind={l.kind}>
                  {`${l.kind} ${l.text}`}
                </div>
              ))}
            </div>
          ) : command ? (
            <div class="cmd" data-testid="chat-tool-command">{`$ ${command}`}</div>
          ) : m.toolInput !== undefined ? (
            <>
              <span class="label">Input</span>
              <pre aria-label={`Input of ${label}`}>{toolInputText(m.toolInput)}</pre>
            </>
          ) : (
            <p class="none">No input recorded.</p>
          )}
          {result ? (
            <>
              <span class="label">{state === 'failed' ? 'Error' : command ? 'Output' : 'Result'}</span>
              <pre aria-label={`Result of ${label}`} data-testid="chat-tool-result">
                {result}
              </pre>
            </>
          ) : state === 'running' ? null : (
            <p class="none">{state === 'unknown' ? 'No result was reported for this call.' : 'It returned nothing.'}</p>
          )}
        </div>
      </details>
    );
  };
}
