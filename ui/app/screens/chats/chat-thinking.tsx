// The agent's reasoning in a chat (decision P89): folded away above the tool call or answer it came before, and while the turn
// runs, the reasoning so far, open, under the typing dots. It is the CLI's own text, redacted and bounded by the core, shown as
// text (never Markdown: reasoning is a scratchpad, not an answer).

import { css, type Handle } from 'remix/component';
import { t } from '../../ui/tokens.ts';

const foldCss = css({
  maxWidth: '760px',
  margin: '2px 0',
  fontSize: t.textSm,
  color: t.textMuted,
  '& > summary': { cursor: 'pointer', width: 'fit-content', minHeight: '24px', display: 'flex', alignItems: 'center', borderRadius: t.rSm },
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

/** The reasoning before a stored row, folded. Live, it is open and says it is still going. */
export function ThinkingFold(handle: Handle<{ text: string; live?: boolean }>) {
  return () => {
    const { text, live } = handle.props;
    if (!text.trim()) return null;
    return (
      <details mix={foldCss} open={live || undefined} data-testid={live ? 'chat-thinking-live' : 'chat-thinking'}>
        <summary>{live ? 'Thinking…' : 'Thinking'}</summary>
        <div>{text}</div>
      </details>
    );
  };
}
