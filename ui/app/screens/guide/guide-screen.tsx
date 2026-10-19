// The Guide: the explanations the pages leave out (the UI audit of 2026-10-19, section F). A list of topics by section, a search,
// and one topic at a time. A page links to its topic with "Learn more" (ui/learn-more.tsx).

import { css, on, type Handle } from 'remix/component';
import { BookMarked, Search } from 'lucide';
import { Empty } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { GUIDE_SECTIONS, searchTopics, topicPath, type Topic } from './model.ts';
import { TopicBody, inline } from './render.tsx';
import { GUIDE_TOPICS, guideTopic } from './topics.ts';

const layoutCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(200px, 260px) minmax(0, 1fr)',
  gap: `${t.s6} ${t.s8}`,
  alignItems: 'start',
  containerType: 'inline-size',
  '@container (max-width: 760px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});

const navCss = css({
  display: 'grid',
  gap: t.s4,
  minWidth: '0',
  '& h2': { margin: '0 0 6px', fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.06em', textTransform: 'uppercase', color: t.textMuted },
  '& ul': { listStyle: 'none', margin: '0', padding: '0', display: 'grid', gap: '2px' },
  '& a': { display: 'block', padding: `6px ${t.s3}`, borderRadius: t.rMd, color: t.text, textDecoration: 'none', fontSize: t.textMd },
  '& a:hover': { background: t.bgHover },
  '& a[aria-current="page"]': { background: t.bgActive, fontWeight: '600' },
});

const searchCss = css({
  position: 'relative',
  '& svg': { position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: t.textFaint, pointerEvents: 'none' },
  '& input': { width: '100%', paddingLeft: '32px' },
});

const mainCss = css({ display: 'grid', gap: t.s4, minWidth: '0', alignContent: 'start' });
const headCss = css({
  display: 'grid',
  gap: t.s1,
  '& h2': { margin: '0', fontSize: t.textXl, fontWeight: '600', letterSpacing: '-0.01em' },
  '& p': { margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '72ch' },
});
const listCss = css({
  listStyle: 'none',
  margin: '0',
  padding: '0',
  display: 'grid',
  gap: '0',
  maxWidth: '72ch',
  '& li': { padding: `${t.s3} 0`, borderTop: `1px solid ${t.border}` },
  '& li:first-child': { borderTop: '0' },
  '& a': { fontWeight: '600', fontSize: t.textMd },
  '& p': { margin: '2px 0 0', color: t.textMuted, fontSize: t.textSm },
});
const relatedCss = css({
  maxWidth: '72ch',
  paddingTop: t.s4,
  borderTop: `1px solid ${t.border}`,
  '& h3': { margin: '0 0 6px', fontSize: t.textSm, fontWeight: '600', color: t.textMuted },
  '& ul': { margin: '0', padding: '0', listStyle: 'none', display: 'flex', flexWrap: 'wrap', gap: `${t.s1} ${t.s4}` },
});

function TopicList(handle: Handle<{ topics: readonly Topic[] }>) {
  return () => (
    <ul mix={listCss}>
      {handle.props.topics.map((x) => (
        <li key={x.id}>
          <a href={topicPath(x.id)}>{x.title}</a>
          <p>{x.summary}</p>
        </li>
      ))}
    </ul>
  );
}

export function GuideScreen(handle: Handle<{ topic?: string }>) {
  let query = '';
  const searchId = `guide-search-${handle.id}`;
  return () => {
    const id = handle.props.topic;
    const topic = guideTopic(id);
    const hits = searchTopics(GUIDE_TOPICS, query);
    const searching = query.trim() !== '';
    return (
      <Page title="Guide" icon={BookMarked} subtitle="How Drydock works, where to set things up, and what the words mean" testId="guide">
        <div mix={layoutCss}>
          <nav aria-label="Guide topics" mix={navCss}>
            <div mix={searchCss}>
              <Icon icon={Search} size={14} />
              <input
                id={searchId}
                type="search"
                aria-label="Search the Guide"
                placeholder="Search the Guide"
                autocomplete="off"
                spellcheck={false}
                value={query}
                mix={on('input', (e) => ((query = e.currentTarget.value), void handle.update()))}
              />
            </div>
            {GUIDE_SECTIONS.map((sec) => (
              <section key={sec.id} aria-label={sec.title}>
                <h2>{sec.title}</h2>
                <ul>
                  {GUIDE_TOPICS.filter((x) => x.section === sec.id).map((x) => (
                    <li key={x.id}>
                      <a href={topicPath(x.id)} aria-current={x.id === id ? 'page' : undefined}>
                        {x.title}
                      </a>
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </nav>
          <div mix={mainCss}>
            {searching ? (
              <section aria-label="Search results" data-testid="guide-results">
                <div mix={headCss}>
                  <h2>{hits.length === 0 ? 'Nothing found' : `${hits.length} ${hits.length === 1 ? 'topic' : 'topics'}`}</h2>
                  <p>{hits.length === 0 ? `No topic mentions “${query.trim()}”. Try fewer or different words.` : `Topics that mention every word of “${query.trim()}”.`}</p>
                </div>
                <TopicList topics={hits.map((h) => h.topic)} />
              </section>
            ) : id !== undefined && !topic ? (
              <Empty title="No such topic" icon={BookMarked}>
                <p>
                  The Guide has no topic called <code>{id}</code>. <a href="/guide">Back to all topics</a>
                </p>
              </Empty>
            ) : topic ? (
              <article aria-labelledby={`topic-${topic.id}`} data-testid="guide-topic" data-topic={topic.id}>
                <div mix={headCss}>
                  <h2 id={`topic-${topic.id}`}>{topic.title}</h2>
                  <p>{topic.summary}</p>
                </div>
                <TopicBody body={topic.body} />
                {topic.related?.length ? (
                  <aside mix={relatedCss} aria-label="Related topics">
                    <h3>Read next</h3>
                    <ul>
                      {topic.related.map((r) => {
                        const other = guideTopic(r);
                        return other ? (
                          <li key={r}>
                            <a href={topicPath(r)}>{inline(other.title)}</a>
                          </li>
                        ) : null;
                      })}
                    </ul>
                  </aside>
                ) : null}
              </article>
            ) : (
              GUIDE_SECTIONS.map((sec) => (
                <section key={sec.id} aria-label={sec.title}>
                  <div mix={headCss}>
                    <h2>{sec.title}</h2>
                    <p>{sec.blurb}</p>
                  </div>
                  <TopicList topics={GUIDE_TOPICS.filter((x) => x.section === sec.id)} />
                </section>
              ))
            )}
          </div>
        </div>
      </Page>
    );
  };
}
