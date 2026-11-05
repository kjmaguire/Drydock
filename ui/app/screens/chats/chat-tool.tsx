// A tool call in a chat as a compact card (decisions P84, P97): an icon of the kind of tool, "Ran" and the command (or the tool's
// readable name and the call) in one line, how long it took and how it went (done, failed, running, or no result reported); opened, the call is shown the way a person reads that kind of tool (an
// edit as a diff, a shell command with its output, a read with what it returned) and anything else as its input and result.
// Everything shown is text the core stored (redacted, bounded); nothing is rendered as HTML.

import { css, type Handle } from 'remix/component';
import { FilePen, FileText, Globe, Search, Terminal, Wrench } from 'lucide';
import type { ChatMessage } from '../../api/chatClient.ts';
import { Spinner } from '../../ui/feedback.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
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

// a compact card (graphite, P97): an icon of the kind of tool, a verb ("Ran" for a command), the call in mono, how long it took
// and how it went on the right (done in teal, failed in red, running with a spinner)
const cardCss = css({
  maxWidth: '720px',
  margin: '2px 0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textMd,
  '&[data-state="failed"]': { borderColor: `color-mix(in srgb, ${t.danger} 40%, ${t.border})` },
  '& > summary': {
    display: 'flex',
    alignItems: 'center',
    gap: '10px',
    minWidth: '0',
    minHeight: '34px',
    padding: '0 12px',
    listStyle: 'none',
    cursor: 'pointer',
    borderRadius: t.rMd,
    color: t.text,
  },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary:hover': { background: t.bgHover },
  '& > summary:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > summary .ic': { display: 'inline-flex', flex: 'none', color: t.textMuted },
  '& > summary strong': { flex: 'none', maxWidth: '45%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: '400' },
  '& > summary .sum': { flex: '1 1 auto', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: t.mono, fontSize: t.textXs, color: t.textMuted },
  '& > summary .ms': { flex: 'none', fontSize: t.textXs, color: t.textFaint, fontVariantNumeric: 'tabular-nums' },
  '& > summary .state': { display: 'inline-flex', alignItems: 'center', gap: '5px', flex: 'none', fontSize: t.textXs, color: t.textFaint },
  '& > summary .state[data-state="ok"]': { color: t.success },
  '& > summary .state[data-state="failed"]': { color: t.danger },
  '& > summary .state[data-state="running"]': { color: t.textMuted },
  '& .body': { display: 'grid', gap: t.s2, padding: `2px 12px 10px`, fontSize: t.textSm },
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

/** The icon of a kind of tool: a terminal for a command, a file for a read, a pen for an edit, a lens for a search. */
function toolIcon(name: string, command: boolean): IconNode {
  if (command || name === 'Bash') return Terminal;
  if (name === 'Read' || name === 'NotebookRead') return FileText;
  if (name === 'Edit' || name === 'MultiEdit' || name === 'Write' || name === 'NotebookEdit') return FilePen;
  if (name === 'Grep' || name === 'Glob') return Search;
  if (name === 'WebFetch' || name === 'WebSearch') return Globe;
  return Wrench;
}

/** What the card says before the call: "Ran" (or "Running") for a shell command, else the tool's readable name. */
export function toolVerb(name: string | undefined, command: boolean, state: ToolState): string {
  if (command || name === 'Bash') return state === 'running' ? 'Running' : 'Ran';
  return toolLabel(name);
}

// a diff on its own (the changes of a turn, chat-burst.tsx): the same lines as in an edit's card
const diffCss = css({
  margin: '0',
  maxHeight: '420px',
  overflow: 'auto',
  fontFamily: t.mono,
  fontSize: t.textXs,
  borderRadius: t.rSm,
  border: `1px solid ${t.border}`,
  '& > div': { padding: '0 6px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& > div[data-kind="-"]': { background: `color-mix(in srgb, ${t.dangerSoft} 80%, transparent)` },
  '& > div[data-kind="+"]': { background: `color-mix(in srgb, ${t.successSoft} 80%, transparent)` },
});

/** Removed and added lines, each marked, as an edit's card shows them. */
export function DiffBlock(handle: Handle<{ lines: readonly DiffLine[]; label: string; testId?: string }>) {
  return () => {
    const { lines, label, testId } = handle.props;
    return (
      <div mix={diffCss} role="group" aria-label={label} data-testid={testId}>
        {lines.map((l, i) => (
          <div key={i} data-kind={l.kind}>
            {`${l.kind} ${l.text}`}
          </div>
        ))}
      </div>
    );
  };
}

/** A tool call's card; `open` opens it when it is first drawn (a failed call shows what went wrong without a click). */
export function ToolCard(handle: Handle<{ m: ChatMessage; summary: string; running: boolean; open?: boolean }>) {
  return () => {
    const { m, summary, running, open } = handle.props;
    const state = toolState(m, running);
    const label = toolLabel(m.toolName);
    const ms = durationText(m.toolMs);
    const f = fields(m.toolInput);
    const diff = editLines(m.toolInput);
    const command = str(f, 'command');
    const shell = !!command || m.toolName === 'Bash';
    const result = m.toolResult ?? '';
    return (
      <details mix={cardCss} open={open || undefined} data-testid="chat-tool" data-state={state} aria-label={`${label}: ${STATE_TEXT[state]}`}>
        <summary>
          <span class="ic" aria-hidden="true">
            <Icon icon={toolIcon(m.toolName ?? '', !!command)} size={14} />
          </span>
          <strong title={m.toolName}>{toolVerb(m.toolName, shell, state)}</strong>
          <span class="sum">{summary}</span>
          {ms ? (
            <span class="ms" data-testid="chat-tool-ms">
              {ms}
            </span>
          ) : null}
          <span class="state" data-state={state}>
            {state === 'running' ? <Spinner label="Running" /> : null}
            {STATE_TEXT[state]}
          </span>
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
