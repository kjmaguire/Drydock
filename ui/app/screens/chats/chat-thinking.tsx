// The agent's reasoning in a chat (decisions P89, P139): folded away above the answer it came before as one quiet line ("Thought
// for 6 s"; before tool calls it is part of the burst's line, chat-burst.tsx), and while the turn runs, the reasoning so far,
// open, under the typing dots. It is the CLI's own text, redacted and bounded by the core, shown as
// text (never Markdown: reasoning is a scratchpad, not an answer).

import { css, type Handle } from 'remix/component';
import { t } from '../../ui/tokens.ts';
import { thoughtText } from './chat-groups.ts';

const foldCss = css({
  maxWidth: '760px',
  margin: '2px 0',
  fontSize: t.textSm,
  color: t.textMuted,
  '& > summary': { cursor: 'pointer', width: 'fit-content', minHeight: '24px', display: 'flex', alignItems: 'center', gap: '4px', borderRadius: t.rSm, listStyle: 'none' },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary::before': { content: '"▸"', fontSize: t.textXs, transition: `transform ${t.fast} ${t.ease}` },
  '&[open] > summary::before': { transform: 'rotate(90deg)' },
  '& > summary:hover': { color: t.text },
  '& > summary:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > div': {
    margin: '4px 0 0',
    padding: `2px 0 2px ${t.s3}`,
    borderLeft: `2px solid ${t.border}`,
    whiteSpace: 'pre-wrap',
    overflowWrap: 'anywhere',
    maxHeight: '320px',
    overflow: 'auto',
  },
});

/** The reasoning before a stored row, folded ("Thought for 6 s": `ms`, 0 when not known). Live, it is open and says it is still going. */
export function ThinkingFold(handle: Handle<{ text: string; live?: boolean; ms?: number }>) {
  return () => {
    const { text, live, ms = 0 } = handle.props;
    if (!text.trim()) return null;
    return (
      <details mix={foldCss} open={live || undefined} data-testid={live ? 'chat-thinking-live' : 'chat-thinking'}>
        <summary>{live ? 'Thinking…' : thoughtText(ms)}</summary>
        <div>{text}</div>
      </details>
    );
  };
}
