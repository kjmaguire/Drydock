// Safe Markdown renderer for text that comes from outside the user's own hands (agent output, room messages, GitHub text,
// issue bodies). docs/ui.md section 1 and brief 16: raw HTML is disabled, remote images are not loaded, links show their
// target, and text can never trigger an action.
//
// Design: marked is used only as a lexer. Its tokens are mapped to Remix elements by this file, so there is no HTML string,
// no innerHTML and no sanitizer to bypass: an HTML token is rendered as literal text, an image token as a text placeholder
// (no <img> element is ever created, so nothing is requested), and a link is an <a> only for http, https and mailto.

import { createElement, css, type Handle, type RemixNode } from 'remix/component';
import { marked, type Token, type Tokens } from 'marked';
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
      return (
        <div class="md-table-wrap">
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

/** Parse Markdown into tokens; exported for tests. */
export function lex(source: string): Token[] {
  return marked.lexer(source, { gfm: true, breaks: false, async: false }) as Token[];
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
  '& .md-link-target': { color: t.textFaint, fontSize: t.textSm },
  '& .md-image-blocked, & .md-link-blocked, & .md-html-text': { color: t.textMuted },
  '& .md-table-wrap': { overflow: 'auto', border: `1px solid ${t.border}`, borderRadius: t.rMd },
  '& table td, & table th': { padding: '4px 8px' },
});

export function Markdown(handle: Handle<{ source: string; maxChars?: number }>) {
  return () => {
    const { source, maxChars = 20000 } = handle.props;
    const clipped = source.length > maxChars;
    const text = clipped ? source.slice(0, maxChars) : source;
    return (
      <div class="md" mix={mdCss}>
        {blocks(lex(text))}
        {clipped ? <p style={{ color: 'var(--text-muted)' }}>[{source.length - maxChars} more characters not shown]</p> : null}
      </div>
    );
  };
}
