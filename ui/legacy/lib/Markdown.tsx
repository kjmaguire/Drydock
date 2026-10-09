// Safe Markdown renderer for text that comes from outside the user's own hands (agent output, room messages, GitHub text,
// issue bodies). docs/ui.md section 1 and brief 16: raw HTML is disabled, remote images are not loaded, links show their
// target, and text can never trigger an action.
//
// Design: marked is used only as a lexer. Its tokens are mapped to React elements by this file, so there is no HTML string,
// no innerHTML and no sanitizer to bypass: an HTML token is rendered as literal text, an image token as a text placeholder
// (no <img> element is ever created, so nothing is requested), and a link is an <a> only for http, https and mailto.

import { Fragment, type ReactNode } from 'react';
import { marked, type Token, type Tokens } from 'marked';

const SAFE_SCHEMES = new Set(['http:', 'https:', 'mailto:']);

/** Returns the normalized href if the link target may be opened, otherwise undefined. */
export function safeHref(raw: string): string | undefined {
  const trimmed = raw.trim();
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

function Inline({ tokens }: { tokens: Token[] | undefined }): ReactNode {
  if (!tokens) return null;
  return (
    <>
      {tokens.map((t, i) => (
        <Fragment key={i}>{inline(t)}</Fragment>
      ))}
    </>
  );
}

function inline(t: Token): ReactNode {
  switch (t.type) {
    case 'text': {
      const tt = t as Tokens.Text;
      return tt.tokens ? <Inline tokens={tt.tokens} /> : tt.text;
    }
    case 'escape':
      return (t as Tokens.Escape).text;
    case 'strong':
      return <strong>{<Inline tokens={(t as Tokens.Strong).tokens} />}</strong>;
    case 'em':
      return <em>{<Inline tokens={(t as Tokens.Em).tokens} />}</em>;
    case 'del':
      return <del>{<Inline tokens={(t as Tokens.Del).tokens} />}</del>;
    case 'codespan':
      return <code>{(t as Tokens.Codespan).text}</code>;
    case 'br':
      return <br />;
    case 'html':
      // raw HTML is shown as literal text, never parsed
      return <span className="md-html-text">{(t as Tokens.HTML).raw}</span>;
    case 'image': {
      const im = t as Tokens.Image;
      return (
        <span className="md-image-blocked" title="Remote images are never loaded">
          [image not loaded{im.text ? `: ${im.text}` : ''}] <code>{clip(im.href, 120)}</code>
        </span>
      );
    }
    case 'link': {
      const l = t as Tokens.Link;
      const href = safeHref(l.href);
      if (!href) {
        return (
          <span className="md-link-blocked">
            <Inline tokens={l.tokens} /> <code title="This link target is not allowed and was not made clickable">(blocked link: {clip(l.href, 120)})</code>
          </span>
        );
      }
      return (
        <>
          <a href={href} target="_blank" rel="noopener noreferrer nofollow" referrerPolicy="no-referrer" title={href}>
            <Inline tokens={l.tokens} />
          </a>{' '}
          <span className="md-link-target">({clip(href, 80)})</span>
        </>
      );
    }
    default:
      return (t as { raw?: string }).raw ?? '';
  }
}

function Blocks({ tokens }: { tokens: Token[] }): ReactNode {
  return (
    <>
      {tokens.map((t, i) => (
        <Fragment key={i}>{block(t)}</Fragment>
      ))}
    </>
  );
}

function block(t: Token): ReactNode {
  switch (t.type) {
    case 'space':
    case 'def':
      return null;
    case 'paragraph':
      return <p>{<Inline tokens={(t as Tokens.Paragraph).tokens} />}</p>;
    case 'text': {
      const tt = t as Tokens.Text;
      return tt.tokens ? <Inline tokens={tt.tokens} /> : tt.text;
    }
    case 'heading': {
      const h = t as Tokens.Heading;
      const level = Math.min(6, h.depth + 2); // message headings must not out-shout the UI
      const Tag = `h${level}` as 'h3';
      return <Tag>{<Inline tokens={h.tokens} />}</Tag>;
    }
    case 'code':
      return (
        <pre className="md-code">
          <code>{(t as Tokens.Code).text}</code>
        </pre>
      );
    case 'blockquote':
      return <blockquote>{<Blocks tokens={(t as Tokens.Blockquote).tokens} />}</blockquote>;
    case 'hr':
      return <hr />;
    case 'html':
      return <pre className="md-html-text">{(t as Tokens.HTML).raw.trimEnd()}</pre>;
    case 'list': {
      const l = t as Tokens.List;
      const items = l.items.map((it, i) => (
        <li key={i}>
          {it.task ? <span aria-hidden="true">{it.checked ? '☑ ' : '☐ '}</span> : null}
          <Blocks tokens={it.tokens} />
        </li>
      ));
      return l.ordered ? <ol start={typeof l.start === 'number' ? l.start : 1}>{items}</ol> : <ul>{items}</ul>;
    }
    case 'table': {
      const tb = t as Tokens.Table;
      return (
        <div className="md-table-wrap">
          <table>
            <thead>
              <tr>
                {tb.header.map((c, i) => (
                  <th key={i} scope="col">
                    <Inline tokens={c.tokens} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {tb.rows.map((r, ri) => (
                <tr key={ri}>
                  {r.map((c, ci) => (
                    <td key={ci}>
                      <Inline tokens={c.tokens} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    }
    default:
      return <p>{(t as { raw?: string }).raw ?? ''}</p>;
  }
}

/** Parse Markdown into tokens; exported for tests. */
export function lex(source: string): Token[] {
  return marked.lexer(source, { gfm: true, breaks: false, async: false }) as Token[];
}

export function Markdown({ source, maxChars = 20000 }: { source: string; maxChars?: number }) {
  const clipped = source.length > maxChars;
  const text = clipped ? source.slice(0, maxChars) : source;
  return (
    <div className="md">
      <Blocks tokens={lex(text)} />
      {clipped ? <p className="muted">[{source.length - maxChars} more characters not shown]</p> : null}
    </div>
  );
}
