// The Guide's content model (the UI audit of 2026-10-19, section F): what a screen used to explain in paragraphs lives here, in
// topics the screens link to ("Learn more"). The text is written by the product, so it is trusted and rendered by guide/render.tsx
// (a small inline syntax, not Markdown from outside). Every link inside it is checked by model.test.ts.

export type GuideSection = 'start' | 'concepts' | 'howto' | 'reference' | 'safety' | 'trouble';

export const GUIDE_SECTIONS: readonly { id: GuideSection; title: string; blurb: string }[] = [
  { id: 'start', title: 'Start here', blurb: 'What Drydock is and how a task travels.' },
  { id: 'concepts', title: 'Concepts', blurb: 'The ideas behind the pages.' },
  { id: 'howto', title: 'How to', blurb: 'Set something up, step by step.' },
  { id: 'reference', title: 'Reference', blurb: 'Words, rules and values, looked up.' },
  { id: 'safety', title: 'Safety and privacy', blurb: 'What stays on this computer and what leaves.' },
  { id: 'trouble', title: 'Troubleshooting', blurb: 'When something does not work.' },
];

/** One block of a topic. Text uses `code`, **bold** and [label](target); a target is a page path (/routing), guide:topic-id or https://. */
export type Block =
  | { p: string }
  | { h: string }
  | { ul: readonly string[] }
  | { ol: readonly string[] }
  | { code: string }
  | { note: string; tone?: 'warn' | 'info' }
  | { table: { head: readonly string[]; rows: readonly (readonly string[])[] } };

export interface Topic {
  id: string;
  section: GuideSection;
  title: string;
  /** One sentence for the topic list and for search results. */
  summary: string;
  /** Words people type that this topic should be found by, besides its text. */
  keywords?: readonly string[];
  body: readonly Block[];
  /** Topics to read next. */
  related?: readonly string[];
}

/** The text of one block as plain words (for search): no syntax characters. */
export function blockText(b: Block): string {
  const plain = (s: string) => s.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1').replace(/[`*]/g, '');
  if ('p' in b) return plain(b.p);
  if ('h' in b) return plain(b.h);
  if ('ul' in b) return b.ul.map(plain).join(' ');
  if ('ol' in b) return b.ol.map(plain).join(' ');
  if ('code' in b) return b.code;
  if ('note' in b) return plain(b.note);
  return [...b.table.head, ...b.table.rows.flat()].map(plain).join(' ');
}

export function topicText(t: Topic): string {
  return [t.title, t.summary, ...(t.keywords ?? []), ...t.body.map(blockText)].join(' ');
}

export function topicPath(id: string): string {
  return `/guide/${encodeURIComponent(id)}`;
}

export interface Hit {
  topic: Topic;
  /** Higher is better: a title match beats a summary match beats a body match. */
  score: number;
}

/** Topics matching every word of the query, best first. An empty query matches nothing. */
export function searchTopics(topics: readonly Topic[], query: string): Hit[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];
  const hits: Hit[] = [];
  for (const topic of topics) {
    const title = topic.title.toLowerCase();
    const summary = topic.summary.toLowerCase();
    const keys = (topic.keywords ?? []).join(' ').toLowerCase();
    const all = topicText(topic).toLowerCase();
    let score = 0;
    let every = true;
    for (const w of words) {
      if (!all.includes(w)) {
        every = false;
        break;
      }
      score += title.includes(w) ? 10 : keys.includes(w) ? 6 : summary.includes(w) ? 4 : 1;
    }
    if (every) hits.push({ topic, score });
  }
  return hits.sort((a, b) => b.score - a.score || a.topic.title.localeCompare(b.topic.title));
}
