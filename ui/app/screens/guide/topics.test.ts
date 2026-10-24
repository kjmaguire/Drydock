import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { parseRoute } from '../../lib/route.ts';
import { GUIDE_SECTIONS, searchTopics, topicPath, type Block, type Topic } from './model.ts';
import { linkTarget } from './render.tsx';
import { GUIDE_IDS, GUIDE_TOPICS, guideTopic } from './topics.ts';

/** Every text of a topic that uses the link syntax. */
function texts(b: Block): string[] {
  if ('p' in b) return [b.p];
  if ('h' in b) return [b.h];
  if ('ul' in b) return [...b.ul];
  if ('ol' in b) return [...b.ol];
  if ('note' in b) return [b.note];
  if ('code' in b) return [];
  return [...b.table.head, ...b.table.rows.flat()];
}
const allTexts = (t: Topic) => [t.title, t.summary, ...t.body.flatMap(texts)];
const links = (s: string) => [...s.matchAll(/\[([^\]]+)\]\(([^)\s]+)\)/g)].map((m) => ({ label: m[1]!, target: m[2]! }));

describe('the Guide content (the UI audit of 2026-10-19)', () => {
  it('has the topics GUIDE_IDS names, once each, and nothing else', () => {
    const ids = GUIDE_TOPICS.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect([...ids].sort()).toEqual([...GUIDE_IDS].sort());
  });

  it('gives every topic a section, a title, a one-line summary and a body, and puts it in the list of its section', () => {
    const sections = new Set(GUIDE_SECTIONS.map((s) => s.id));
    const problems: string[] = [];
    for (const t of GUIDE_TOPICS) {
      if (!sections.has(t.section)) problems.push(`${t.id}: unknown section ${t.section}`);
      if (t.title.trim() === '') problems.push(`${t.id}: no title`);
      if (t.summary.length <= 20 || t.summary.length > 140) problems.push(`${t.id}: the summary is ${t.summary.length} characters, not 21 to 140`);
      if (t.body.length === 0) problems.push(`${t.id}: no body`);
    }
    for (const s of GUIDE_SECTIONS) if (!GUIDE_TOPICS.some((t) => t.section === s.id)) problems.push(`section ${s.id} has no topic`);
    expect(problems).toEqual([]);
  });

  it('points every related topic and every guide: link at a topic that exists', () => {
    const problems: string[] = [];
    for (const t of GUIDE_TOPICS) {
      for (const r of t.related ?? []) {
        if (r === t.id) problems.push(`${t.id}: related to itself`);
        if (!guideTopic(r)) problems.push(`${t.id}: related ${r} does not exist`);
      }
      for (const text of allTexts(t)) {
        for (const l of links(text)) {
          if (l.target.startsWith('guide:') && !guideTopic(l.target.slice(6))) problems.push(`${t.id}: link ${l.target} does not exist`);
        }
      }
    }
    expect(problems).toEqual([]);
  });

  it('links to pages the app has and to https addresses only', () => {
    const problems: string[] = [];
    for (const t of GUIDE_TOPICS) {
      for (const text of allTexts(t)) {
        for (const l of links(text)) {
          const to = linkTarget(l.target);
          if (!to) {
            problems.push(`${t.id}: link ${l.target} is not allowed`);
            continue;
          }
          if (!to.external && parseRoute(to.href.split('#')[0]!).name === 'not-found') problems.push(`${t.id}: ${to.href} is not a page`);
        }
      }
    }
    expect(problems).toEqual([]);
  });

  it('writes no HTML and no raw address outside a link or a code span', () => {
    const problems: string[] = [];
    for (const t of GUIDE_TOPICS) {
      for (const text of allTexts(t)) {
        const bare = text.replace(/`[^`]*`/g, '').replace(/\[[^\]]+\]\([^)]+\)/g, '');
        if (/<[a-z/]/i.test(bare)) problems.push(`${t.id}: markup in "${text.slice(0, 50)}"`);
        if (/https?:\/\//.test(bare)) problems.push(`${t.id}: a bare address in "${text.slice(0, 50)}"`);
      }
    }
    expect(problems).toEqual([]);
  });

  it('refuses a link whose target is not a page, a topic or https', () => {
    expect(linkTarget('javascript:alert(1)')).toBeUndefined();
    expect(linkTarget('http://example.com')).toBeUndefined();
    expect(linkTarget('//example.com')).toBeUndefined();
    expect(linkTarget('/a b')).toBeUndefined();
    expect(linkTarget('guide:scan')).toEqual({ href: '/guide/scan', external: false });
    expect(linkTarget('https://cli.github.com')).toEqual({ href: 'https://cli.github.com', external: true });
  });

  it('is a route: /guide and /guide/<topic> open the Guide', () => {
    expect(parseRoute('/guide')).toEqual({ name: 'guide' });
    expect(parseRoute(topicPath('scan'))).toEqual({ name: 'guide', topic: 'scan' });
    expect(parseRoute('/guide/a/b').name).toBe('not-found');
  });

  it('finds a topic by every word of the query, best match first, and nothing for an empty query', () => {
    expect(searchTopics(GUIDE_TOPICS, '')).toEqual([]);
    expect(searchTopics(GUIDE_TOPICS, '   ')).toEqual([]);
    const scan = searchTopics(GUIDE_TOPICS, 'scan repository');
    expect(scan[0]!.topic.id).toBe('scan');
    expect(searchTopics(GUIDE_TOPICS, 'gitleaks osv').map((h) => h.topic.id)).toContain('scan');
    expect(searchTopics(GUIDE_TOPICS, 'zzzz-nothing-matches')).toEqual([]);
    // a word of the title beats the same word deep in a body
    expect(searchTopics(GUIDE_TOPICS, 'budgets')[0]!.topic.id).toBe('budgets');
  });

  it('keeps the rule-set licences in step with the Security page (one source)', () => {
    const t = guideTopic('rule-licences')!;
    const table = t.body.find((b): b is Extract<Block, { table: unknown }> => 'table' in b)!;
    expect(table.table.rows.length).toBe(5);
    expect(JSON.stringify(table.table.rows)).toContain('Commons Clause');
  });

  it('sends people to Integrations to add an MCP server, not to the Library (decision P98)', () => {
    for (const id of ['integrations', 'github'] as const) {
      const text = allTexts(guideTopic(id)!).join('\n');
      expect([id, /MCP server[^.]*Library|Library[^.]*MCP server/.test(text)]).toEqual([id, false]);
    }
    const text = allTexts(guideTopic('integrations')!).join('\n');
    expect(text).toContain('**Add MCP server**');
    expect(text).toContain('**Your servers**');
    expect(searchTopics(GUIDE_TOPICS, 'add mcp server')[0]!.topic.id).toBe('integrations');
  });
});
