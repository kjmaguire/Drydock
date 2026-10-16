import { css, on, type Handle, type Props } from 'remix/component';
import { visible } from '../lib/visible.ts';
import { t } from './tokens.ts';

// Text that someone else wrote - a tool's description from an MCP server - and that one server could make as long as it likes
// (audit 2026-10-16, L48). It shows the start (about 300 characters, a few lines), the whole text in the tooltip, and a button
// that opens all of it in a box that scrolls: one server can not fill the page. Characters that are not drawn are written out.

/** Characters shown before "Show more". */
export const CLIP_CHARS = 300;
/** Lines shown before "Show more". */
export const CLIP_LINES = 6;
/** The most the tooltip carries. */
const TITLE_CHARS = 2000;

/** The start of `text`: at most `chars` characters and `lines` lines, cut at a space when there is one near the end, with an ellipsis when something was left out. */
export function clipText(text: string, chars = CLIP_CHARS, lines = CLIP_LINES): { head: string; clipped: boolean } {
  const rows = text.split('\n');
  let head = rows.length > lines ? rows.slice(0, lines).join('\n') : text;
  let clipped = rows.length > lines;
  if (head.length > chars) {
    let cut = head.lastIndexOf(' ', chars);
    if (cut < chars * 0.6) cut = chars;
    const high = head.charCodeAt(cut - 1);
    if (high >= 0xd800 && high <= 0xdbff) cut--; // not between the halves of a surrogate pair
    head = head.slice(0, cut);
    clipped = true;
  }
  return { head: clipped ? `${head.trimEnd()}…` : head, clipped };
}

const clipCss = css({
  overflowWrap: 'anywhere',
  '& .clip-more': {
    display: 'inline',
    minHeight: '0',
    height: 'auto',
    margin: '0 0 0 6px',
    padding: '0',
    border: '0',
    borderRadius: '0',
    background: 'none',
    color: t.accentText,
    fontSize: t.textSm,
    fontWeight: '500',
    whiteSpace: 'normal',
    '&:hover': { background: 'none', textDecoration: 'underline' },
  },
  '& .clip-full': { maxHeight: '240px', overflow: 'auto', whiteSpace: 'pre-wrap' },
});

/** A block of outside text, clipped (see above). `mix` styles the block as the text beside it is styled. */
export function ClippedText(handle: Handle<Props<'div'> & { text: string; max?: number; lines?: number }>) {
  let open = false;
  return () => {
    const { text, max = CLIP_CHARS, lines = CLIP_LINES, mix, ...rest } = handle.props;
    const given: unknown = text; // from a server: not always the string the type promises
    const shown = visible(typeof given === 'string' ? given : given == null ? '' : String(given));
    const { head, clipped } = clipText(shown, max, lines);
    if (!clipped) {
      return (
        <div {...rest} mix={[clipCss, ...(mix ?? [])]}>
          {shown}
        </div>
      );
    }
    return (
      <div {...rest} mix={[clipCss, ...(mix ?? [])]} title={open ? undefined : shown.slice(0, TITLE_CHARS)} data-clipped={open ? 'open' : 'closed'}>
        {open ? <div class="clip-full">{shown}</div> : head}
        <button
          type="button"
          class="clip-more"
          aria-expanded={open}
          mix={on('click', () => {
            open = !open;
            void handle.update();
          })}
        >
          {open ? 'Show less' : 'Show more'}
        </button>
      </div>
    );
  };
}
