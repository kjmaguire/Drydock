// Draws a Guide topic. The text is the product's own (guide/model.ts): `code`, **bold** and [label](target) are the only syntax.
// A target is a page of the app ("/routing"), another topic ("guide:routing-rules") or an https address (opens in a new tab).

import { css, type Handle, type RemixNode } from 'remix/component';
import { t } from '../../ui/tokens.ts';
import type { Block } from './model.ts';
import { topicPath } from './model.ts';

/** Where a link target leads: an app path, or an external address. */
export function linkTarget(target: string): { href: string; external: boolean } | undefined {
  if (target.startsWith('guide:')) return { href: topicPath(target.slice('guide:'.length)), external: false };
  if (/^\/[a-z0-9/_-]*(#[a-z0-9-]+)?$/i.test(target)) return { href: target, external: false };
  if (/^https:\/\/[a-z0-9.-]+(\/[^\s]*)?$/i.test(target)) return { href: target, external: true };
  return undefined;
}

const TOKEN = /`([^`]+)`|\*\*([^*]+)\*\*|\[([^\]]+)\]\(([^)\s]+)\)/g;

/** The text with its syntax drawn; a link whose target is not allowed is shown as its label alone. */
export function inline(text: string): RemixNode[] {
  const out: RemixNode[] = [];
  let at = 0;
  for (const m of text.matchAll(TOKEN)) {
    if (m.index > at) out.push(text.slice(at, m.index));
    if (m[1] !== undefined) out.push(<code>{m[1]}</code>);
    else if (m[2] !== undefined) out.push(<strong>{m[2]}</strong>);
    else {
      const to = linkTarget(m[4]!);
      if (!to) out.push(m[3]!);
      else if (to.external) {
        out.push(
          <a href={to.href} target="_blank" rel="noopener noreferrer" data-rmx-document>
            {m[3]}
          </a>,
        );
      } else out.push(<a href={to.href}>{m[3]}</a>);
    }
    at = m.index + m[0].length;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

const bodyCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  maxWidth: '72ch',
  fontSize: t.textMd,
  lineHeight: '1.6',
  '& h3': { margin: `${t.s3} 0 0`, fontSize: t.textLg, fontWeight: '600' },
  '& p, & ul, & ol': { margin: '0' },
  '& ul, & ol': { paddingLeft: t.s5, display: 'grid', gap: t.s2 },
  '& pre': { margin: '0', padding: t.s3, background: t.bgRaised, border: `1px solid ${t.border}`, borderRadius: t.rMd, overflowX: 'auto', whiteSpace: 'pre', fontSize: t.textSm },
  '& .note': { margin: '0', padding: `${t.s3} ${t.s4}`, border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgRaised },
  '& .note.warn': { borderColor: t.warning },
  '& .tablewrap': { overflowX: 'auto', border: `1px solid ${t.border}`, borderRadius: t.rMd },
  '& table': { width: '100%', borderCollapse: 'collapse', fontSize: t.textSm },
  '& th, & td': { textAlign: 'left', verticalAlign: 'top', padding: `${t.s2} ${t.s4}`, borderBottom: `1px solid ${t.border}` },
  '& tr:last-child td': { borderBottom: '0' },
  '& th': { fontWeight: '600', color: t.textMuted, whiteSpace: 'nowrap' },
});

export function TopicBody(handle: Handle<{ body: readonly Block[] }>) {
  return () => (
    <div mix={bodyCss}>
      {handle.props.body.map((b) => {
        if ('p' in b) return <p>{inline(b.p)}</p>;
        if ('h' in b) return <h3>{inline(b.h)}</h3>;
        if ('ul' in b) return <ul>{b.ul.map((x) => <li>{inline(x)}</li>)}</ul>;
        if ('ol' in b) return <ol>{b.ol.map((x) => <li>{inline(x)}</li>)}</ol>;
        if ('code' in b) return <pre><code>{b.code}</code></pre>;
        if ('note' in b) return <p class={`note ${b.tone ?? 'info'}`}>{inline(b.note)}</p>;
        return (
          <div class="tablewrap" tabIndex={0} role="region" aria-label="Table">
            <table>
              <thead>
                <tr>{b.table.head.map((h) => <th>{h}</th>)}</tr>
              </thead>
              <tbody>
                {b.table.rows.map((r) => (
                  <tr>{r.map((c) => <td>{inline(c)}</td>)}</tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      })}
    </div>
  );
}
