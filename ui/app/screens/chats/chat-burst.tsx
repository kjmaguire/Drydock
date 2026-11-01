// The calm chat's quiet rows (decision P139; the rules are in chat-groups.ts):
//   - BurstLine: a run of tool calls as one line ("Read 6 files, ran 3 commands · 4.2 s"), opened to today's tool cards;
//   - ChangesCard: the files one turn changed ("Changed 2 files +12 −3") with Review changes, which opens the changes as diffs;
//   - ReplyChips: the replies offered after the agent's final answer.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { ChevronRight, FilePen } from 'lucide';
import type { ChatMessage } from '../../api/chatClient.ts';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Spinner } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { changedText, stepText, type Reply, type TurnChanges } from './chat-groups.ts';
import { DiffBlock, durationText } from './chat-tool.tsx';

const burstCss = css({
  display: 'grid',
  gap: '6px',
  maxWidth: '720px',
  padding: '3px 0',
  '& > .line': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    width: 'fit-content',
    maxWidth: '100%',
    minHeight: '24px',
    padding: '0 4px 0 0',
    margin: '0',
    border: '0',
    borderRadius: t.rSm,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '400',
    textAlign: 'left',
  },
  '& > .line:hover': { color: t.text },
  '& > .line:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > .line svg': { flex: 'none', transition: `transform ${t.fast} ${t.ease}` },
  '& > .line[aria-expanded="true"] > svg': { transform: 'rotate(90deg)' },
  '& > .line .txt': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > .line .ms': { flex: 'none', fontVariantNumeric: 'tabular-nums' },
  '& > .line .step': { display: 'inline-flex', alignItems: 'center', gap: '5px', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: t.mono, fontSize: t.textXs },
  '& > .line .failed': { flex: 'none', color: t.danger, fontWeight: '500' },
  '& > .calls': { display: 'grid', gap: '2px', paddingLeft: '14px', borderLeft: `2px solid ${t.border}`, marginLeft: '5px' },
});

/**
 * A run of tool calls as one quiet line. `children` are the calls' own rows, drawn only while it is open; the line says the
 * current step while the run goes on and how many failed.
 */
export function BurstLine(
  handle: Handle<{ tools: ChatMessage[]; line: string; state: 'running' | 'failed' | 'ok'; expanded: boolean; onToggle: () => void; children?: RemixNode }>,
) {
  return () => {
    const { tools, line, state, expanded, onToggle, children } = handle.props;
    const ms = durationText(tools.reduce((a, m) => a + (m.toolMs ?? 0), 0));
    const failed = tools.filter((m) => m.toolStatus === 'failed').length;
    // the step a running run is on: its newest call without a result
    const current = state === 'running' ? [...tools].reverse().find((m) => !m.toolStatus) : undefined;
    return (
      <div mix={burstCss} data-testid="chat-burst" data-state={state} data-expanded={expanded ? 'true' : 'false'} data-calls={String(tools.length)}>
        <button type="button" class="line" aria-expanded={expanded ? 'true' : 'false'} data-testid="chat-burst-toggle" mix={on('click', () => onToggle())} title={expanded ? 'Hide the tool calls' : 'Show the tool calls'}>
          <Icon icon={ChevronRight} size={13} />
          <span class="txt">{line}</span>
          {ms && state !== 'running' ? <span class="ms">· {ms}</span> : null}
          {failed ? <span class="failed">· {failed} failed</span> : null}
          {current ? (
            <span class="step" data-testid="chat-burst-step">
              <Spinner label="Running" />
              {stepText(current)}
            </span>
          ) : null}
        </button>
        {expanded ? (
          <div class="calls" role="group" aria-label="Tool calls">
            {children}
          </div>
        ) : null}
      </div>
    );
  };
}

const changesCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: '10px',
  maxWidth: '720px',
  margin: '4px 0',
  padding: '8px 10px 8px 12px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textMd,
  '& > svg': { flex: 'none', color: t.textMuted },
  '& .what': { fontWeight: '500' },
  '& .add': { color: t.success, fontFamily: t.mono, fontSize: t.textSm },
  '& .del': { color: t.danger, fontFamily: t.mono, fontSize: t.textSm },
  '& .files': { flex: '1 1 160px', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs },
});
const fileCss = css({
  display: 'grid',
  gap: '6px',
  '& + &': { marginTop: t.s4 },
  '& h3': { display: 'flex', alignItems: 'center', gap: '10px', margin: '0', fontSize: t.textSm, fontWeight: '500', fontFamily: t.mono, overflowWrap: 'anywhere' },
  '& .add': { color: t.success },
  '& .del': { color: t.danger },
});

const base = (p: string) => p.split(/[\\/]/).pop() || p;

/** The files one turn changed, in one card; Review changes opens their diffs. */
export function ChangesCard(handle: Handle<{ changes: TurnChanges }>) {
  let open = false;
  return () => {
    const { changes } = handle.props;
    const names = changes.files.map((f) => base(f.path));
    return (
      <div mix={changesCss} role="group" aria-label={`${changedText(changes)}, ${changes.added} lines added, ${changes.removed} removed`} data-testid="chat-changes">
        <Icon icon={FilePen} size={15} />
        <span class="what">{changedText(changes)}</span>
        <span class="add" aria-hidden="true">
          +{changes.added}
        </span>
        <span class="del" aria-hidden="true">
          −{changes.removed}
        </span>
        <span class="files" title={changes.files.map((f) => f.path).join('\n')}>
          {names.join(', ')}
        </span>
        <Button size="sm" variant="secondary" data-testid="chat-changes-review" mix={on('click', () => ((open = true), void handle.update()))}>
          Review changes
        </Button>
        <Dialog open={open} title={changedText(changes)} wide onClose={() => ((open = false), void handle.update())}>
          <div data-testid="chat-changes-files">
            {changes.files.map((f) => (
              <section key={f.path} mix={fileCss} data-testid="chat-changes-file" data-path={f.path}>
                <h3>
                  <span>{f.path}</span>
                  <span class="add">+{f.added}</span>
                  <span class="del">−{f.removed}</span>
                </h3>
                <DiffBlock lines={f.lines} label={`Changes to ${f.path}`} />
              </section>
            ))}
          </div>
        </Dialog>
      </div>
    );
  };
}

const repliesCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '6px',
  padding: '6px 0 2px',
  '& button': {
    height: '28px',
    minHeight: '0',
    padding: '0 12px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: t.bgSurface,
    color: t.text,
    fontSize: t.textSm,
  },
  '& button:hover': { background: t.bgHover, borderColor: t.borderStrong },
  '& button:disabled': { opacity: '0.5' },
});

/** The replies offered after an answer; a click sends that reply. */
export function ReplyChips(handle: Handle<{ replies: Reply[]; disabled: boolean; onReply: (text: string) => void }>) {
  return () => {
    const { replies, disabled, onReply } = handle.props;
    if (!replies.length) return null;
    return (
      <div mix={repliesCss} role="group" aria-label="Suggested replies" data-testid="chat-replies">
        {replies.map((r) => (
          <button key={r.label} type="button" disabled={disabled} title={r.text} data-testid="chat-reply" mix={on('click', () => onReply(r.text))}>
            {r.label}
          </button>
        ))}
      </div>
    );
  };
}
