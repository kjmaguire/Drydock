// Safe Markdown renderer for text that comes from outside the user's own hands (agent output, room messages, GitHub text,
// issue bodies). docs/ui.md section 1 and brief 16: raw HTML is disabled, remote images are not loaded, links show their
// target, and text can never trigger an action.
//
// Design: marked is used only as a lexer. Its tokens are mapped to Remix elements by this file, so there is no HTML string,
// no innerHTML and no sanitizer to bypass: an HTML token is rendered as literal text, an image token as a text placeholder
// (no <img> element is ever created, so nothing is requested), and a link is an <a> only for http, https and mailto.
//
// The lexing runs inside the limits of markdown-lex.ts: crafted text can neither freeze the window nor leave it blank. A block
// or run the limits do not allow is shown as the text that was written, never dropped, and if anything here throws the whole
// message is shown as plain text with the error's text beside it.

import { createElement, css, on, type Handle, type RemixNode } from 'remix/component';
import type { Token, Tokens } from 'marked';
import { errorText } from '../lib/failure.ts';
import { DEFAULT_CHARS, lexSafe, limitsFor, weight, type MarkdownLimits } from './markdown-lex.ts';
import { t } from './tokens.ts';

const SAFE_SCHEMES = new Set(['http:', 'https:', 'mailto:']);

/** Returns the normalized href if the link target may be opened, otherwise undefined. */
export function safeHref(raw: string): string | undefined {
  const trimmed = raw.trim();
  // eslint-disable-next-line no-control-regex
  if (!trimmed || /[\u0000-\u001f\u007f]/.test(trimmed)) return undefined;
  try {
    const u = new URL(trimmed); // no base: relative and protocol-relative links are not openable
    return SAFE_SCHEMES.has(u.protocol) ? u.href : undefined;
  } catch {
    return undefined;
  }
}

function clip(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
}

function inlines(tokens: Token[] | undefined): RemixNode {
  if (!tokens) return null;
  return tokens.map((tk) => inline(tk));
}

function inline(tk: Token): RemixNode {
  switch (tk.type) {
    case 'text': {
      const tt = tk as Tokens.Text;
      return tt.tokens ? inlines(tt.tokens) : tt.text;
    }
    case 'escape':
      return (tk as Tokens.Escape).text;
    case 'strong':
      return <strong>{inlines((tk as Tokens.Strong).tokens)}</strong>;
    case 'em':
      return <em>{inlines((tk as Tokens.Em).tokens)}</em>;
    case 'del':
      return <del>{inlines((tk as Tokens.Del).tokens)}</del>;
    case 'codespan':
      return <code>{(tk as Tokens.Codespan).text}</code>;
    case 'br':
      return <br />;
    case 'more':
      return <Rest text={(tk as Tokens.Generic).text as string} />;
    case 'html':
      // raw HTML is shown as literal text, never parsed
      return <span class="md-html-text">{(tk as Tokens.HTML).raw}</span>;
    case 'image': {
      const im = tk as Tokens.Image;
      return (
        <span class="md-image-blocked" title="Remote images are never loaded">
          [image not loaded{im.text ? `: ${im.text}` : ''}] <code>{clip(im.href, 120)}</code>
        </span>
      );
    }
    case 'link': {
      const l = tk as Tokens.Link;
      const href = safeHref(l.href);
      if (!href) {
        return (
          <span class="md-link-blocked">
            {inlines(l.tokens)} <code title="This link target is not allowed and was not made clickable">(blocked link: {clip(l.href, 120)})</code>
          </span>
        );
      }
      return (
        <>
          <a href={href} target="_blank" rel="noopener noreferrer nofollow" referrerPolicy="no-referrer" title={href} data-rmx-document>
            {inlines(l.tokens)}
          </a>{' '}
          <span class="md-link-target">({clip(href, 80)})</span>
        </>
      );
    }
    default:
      return (tk as { raw?: string }).raw ?? '';
  }
}

function blocks(tokens: Token[]): RemixNode {
  return tokens.map((tk) => block(tk));
}

function block(tk: Token): RemixNode {
  switch (tk.type) {
    case 'space':
    case 'def':
      return null;
    case 'paragraph':
      return <p>{inlines((tk as Tokens.Paragraph).tokens)}</p>;
    case 'text': {
      const tt = tk as Tokens.Text;
      return tt.tokens ? inlines(tt.tokens) : tt.text;
    }
    case 'heading': {
      const h = tk as Tokens.Heading;
      const level = Math.min(6, h.depth + 2); // message headings must not out-shout the UI
      return createElement(`h${level}`, {}, inlines(h.tokens));
    }
    case 'code':
      return (
        <pre class="md-code">
          <code>{(tk as Tokens.Code).text}</code>
        </pre>
      );
    case 'blockquote':
      return <blockquote>{blocks((tk as Tokens.Blockquote).tokens)}</blockquote>;
    case 'hr':
      return <hr />;
    case 'plain':
      return <pre class="md-plain">{(tk as Tokens.Generic).text as string}</pre>;
    case 'html':
      return <pre class="md-html-text">{(tk as Tokens.HTML).raw.trimEnd()}</pre>;
    case 'list': {
      const l = tk as Tokens.List;
      const items = l.items.map((it) => (
        <li>
          {it.task ? <span aria-hidden="true">{it.checked ? '☑ ' : '☐ '}</span> : null}
          {blocks(it.tokens)}
        </li>
      ));
      return l.ordered ? <ol start={typeof l.start === 'number' ? l.start : 1}>{items}</ol> : <ul>{items}</ul>;
    }
    case 'table': {
      const tb = tk as Tokens.Table;
      // a wide table scrolls: the wrapper is a named, focusable group so the keyboard can scroll it (WCAG 2.1.1). A group, not
      // a region: a page with several tables must not get several landmarks of the same kind.
      const heads = tb.header.map((c) => c.text.trim()).filter(Boolean);
      return (
        <div class="md-table-wrap" role="group" aria-label={clip(heads.length ? `Table: ${heads.join(', ')}` : 'Table', 100)} tabIndex={0}>
          <table>
            <thead>
              <tr>
                {tb.header.map((c) => (
                  <th scope="col">{inlines(c.tokens)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {tb.rows.map((r) => (
                <tr>
                  {r.map((c) => (
                    <td>{inlines(c.tokens)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    }
    default:
      return <p>{(tk as { raw?: string }).raw ?? ''}</p>;
  }
}

/** Parse Markdown into tokens within the limits of markdown-lex.ts; exported for tests. Never throws. */
export function lex(source: string, limits: MarkdownLimits = limitsFor(DEFAULT_CHARS)): Token[] {
  return lexSafe(source, limits);
}

/**
 * A lexer that remembers its last answer: one per Markdown instance, so re-rendering a list of messages (any event, a
 * scroll, a hover) re-lexes only the messages whose text changed.
 */
export function memoLex(lexer: (source: string, limits: MarkdownLimits) => Token[] = lex): (source: string, limits?: MarkdownLimits) => Token[] {
  let last: string | undefined;
  let lastTotal = 0;
  let tokens: Token[] = [];
  return (source, limits = limitsFor(DEFAULT_CHARS)) => {
    if (source !== last || limits.totalCost !== lastTotal) {
      tokens = lexer(source, limits);
      last = source;
      lastTotal = limits.totalCost;
    }
    return tokens;
  };
}

/**
 * The blocks to draw, within the element budget: blocks are drawn while they fit; the first one that does not, and everything
 * after it, is one plain text block (the source as it was written).
 */
function draw(tokens: Token[], budget: number): RemixNode {
  const out: RemixNode[] = [];
  let used = 0;
  let rest: string | undefined;
  for (const tk of tokens) {
    if (rest !== undefined) {
      rest += tk.raw;
      continue;
    }
    const w = weight(tk);
    if (used + w > budget) rest = tk.raw;
    else {
      used += w;
      out.push(block(tk));
    }
  }
  if (rest !== undefined && rest.trim()) out.push(<pre class="md-plain">{rest.replace(/\n+$/, '')}</pre>);
  return out;
}

/** Shows the rest of a run that was lexed only in part, as the text that was written, once asked for. */
function Rest(handle: Handle<{ text: string }>) {
  let open = false;
  return () => {
    const { text } = handle.props;
    if (open) return <span class="md-rest">{text}</span>;
    return (
      <button
        type="button"
        class="md-more"
        mix={on('click', () => {
          open = true;
          void handle.update();
        })}
      >
        Show {text.length} more character{text.length === 1 ? '' : 's'}
      </button>
    );
  };
}

const mdCss = css({
  fontSize: t.textMd,
  lineHeight: '1.55',
  overflowWrap: 'anywhere',
  '& > * + *': { marginTop: '6px' },
  '& p': { margin: '0' },
  '& ul, & ol': { margin: '0', paddingLeft: '20px' },
  '& h3, & h4, & h5, & h6': { fontSize: t.textMd, fontWeight: '600', margin: '0' },
  '& blockquote': { margin: '0', paddingLeft: t.s3, borderLeft: `3px solid ${t.borderStrong}`, color: t.textMuted },
  '& pre': { maxHeight: '420px' },
  '& .md-plain': { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& .md-more': {
    display: 'inline',
    minHeight: '0',
    height: 'auto',
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
  '& .md-link-target': { color: t.textFaint, fontSize: t.textSm },
  '& .md-image-blocked, & .md-link-blocked, & .md-html-text': { color: t.textMuted },
  '& .md-table-wrap': { overflow: 'auto', border: `1px solid ${t.border}`, borderRadius: t.rMd },
  '& table td, & table th': { padding: '4px 8px' },
});

/** `lexer` replaces the lexer: for tests of what happens when lexing or drawing throws (a labelled test double). */
export function Markdown(handle: Handle<{ source: string; maxChars?: number; lexer?: (source: string, limits: MarkdownLimits) => Token[] }>) {
  let tokensOf = memoLex();
  let lexer: ((source: string, limits: MarkdownLimits) => Token[]) | undefined;
  let reported: string | undefined;
  return () => {
    const { maxChars = DEFAULT_CHARS } = handle.props;
    // the text comes from the core: a body that is not text (a field missing or null in a malformed message) is shown as what it is
    // rather than throwing here, outside the guard below, and blanking the page
    const given: unknown = handle.props.source;
    const source = typeof given === 'string' ? given : given == null ? '' : String(given);
    if (handle.props.lexer !== lexer) {
      lexer = handle.props.lexer;
      tokensOf = memoLex(lexer);
    }
    const clipped = source.length > maxChars;
    const text = clipped ? source.slice(0, maxChars) : source;
    let body: RemixNode;
    try {
      const limits = limitsFor(maxChars);
      body = draw(tokensOf(text, limits), limits.nodes);
    } catch (err) {
      // nothing in this file should throw; if it does, the message is still there to read, as the text that was written
      if (reported !== text) {
        reported = text;
        console.error('Markdown could not be drawn; the text is shown as written', err);
      }
      body = (
        <>
          <pre class="md-plain" data-testid="md-fallback">
            {text}
          </pre>
          <p class="md-link-target" role="note">
            Shown as written: the formatting failed ({errorText(err)}).
          </p>
        </>
      );
    }
    return (
      <div class="md" mix={mdCss}>
        {body}
        {clipped ? <p style={{ color: t.textMuted }}>[{source.length - maxChars} more characters not shown]</p> : null}
      </div>
    );
  };
}
