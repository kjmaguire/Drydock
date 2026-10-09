// Expert packs: bodies of reference text (documentation, standards) that agents search and cite. Everything inside a pack is
// outside text: it is shown inert, passages that look like instructions are quarantined until the human releases them, and
// nothing in a pack can grant a permission or make a decision.

import { css, on, type Handle } from 'remix/component';
import { BookOpen, Download, ExternalLink, Library, MessageSquareText, RefreshCw, Search, ShieldAlert, Trash2 } from 'lucide';
import type { Pack, PackAttachment, PackCitation, PackConsult, PackSearchResult, QuarantinedChunk } from '../../api/extTypes.ts';
import { formatCost, formatDateTime } from '../../lib/format.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, spacer, stack } from '../../ui/layout.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { attachedText, freshnessText, packIds, refreshText, searchHeadline, zeroTime } from './packs.ts';

// --- citations and results (pack text is untrusted) ---

const citeCss = css({
  display: 'inline-flex',
  alignItems: 'baseline',
  gap: '6px',
  flexWrap: 'wrap',
  fontSize: t.textSm,
  '& a': { display: 'inline-flex', alignItems: 'center', gap: '3px', fontWeight: '500' },
});

/** A citation as the pack search returned it. The URL is outside text: linked only when it is http or https. */
export function CitationLine(handle: Handle<{ c: PackCitation }>) {
  return () => {
    const c = handle.props.c;
    const href = safeHref(c.url);
    return (
      <span mix={citeCss} data-testid="citation">
        <code>
          {c.pack}@{c.version}
        </code>
        {href ? (
          <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document title={href}>
            {c.headingPath || c.url}
            <Icon icon={ExternalLink} size={11} />
          </a>
        ) : (
          <span title="Local or non-web source">{c.headingPath || c.url}</span>
        )}
        <span mix={muted}>
          {c.origin}
          {!zeroTime(c.fetchedAt) ? `, fetched ${formatDateTime(c.fetchedAt)}` : ''}, hash {c.contentHash.slice(0, 8)}
        </span>
      </span>
    );
  };
}

const resultCss = css({ display: 'grid', gap: t.s3 });
const hitsCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: t.s2, padding: `${t.s3} ${t.s4}`, border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgSurface, minWidth: '0' },
});
const headlineCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontSize: t.textMd });

export function SearchResultView(handle: Handle<{ r: PackSearchResult }>) {
  return () => {
    const r = handle.props.r;
    const hits = r.hits ?? [];
    return (
      <div mix={resultCss} data-testid="pack-search-result" data-found={String(r.found)}>
        <p mix={headlineCss}>
          <strong>{searchHeadline(r)}</strong>
          <span>confidence</span> <Badge tone={r.confidence === 'high' ? 'ok' : r.confidence === 'none' ? 'muted' : 'warn'}>{r.confidence}</Badge>
          <span mix={faint}>(a heuristic from the search, not calibrated); source age {r.sourceAgeDays} day(s)</span>
        </p>
        {Object.entries(r.unavailable ?? {}).map(([k, v]) => (
          <Notice key={k} tone="warn">
            {k} unavailable: {v}
          </Notice>
        ))}
        {(r.warn ?? []).map((w, i) => (
          <Notice key={i} tone="warn">
            {w}
          </Notice>
        ))}
        {(r.suspiciousDropped ?? 0) > 0 ? <Notice tone="warn">{r.suspiciousDropped} result(s) were dropped because they looked like instructions to an agent.</Notice> : null}
        {hits.length > 0 ? (
          <ol mix={hitsCss}>
            {hits.map((h, i) => (
              <li key={i} data-testid="pack-hit">
                <CitationLine c={h.citation} />
                <Markdown source={h.snippet || h.text} maxChars={4000} />
              </li>
            ))}
          </ol>
        ) : null}
        {(r.attributions ?? []).length > 0 ? <p mix={faint}>Attribution: {(r.attributions ?? []).join('; ')}</p> : null}
      </div>
    );
  };
}

const answerCss = css({ padding: t.s4, border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgSurface });
const sourcesCss = css({ display: 'grid', gap: t.s2, margin: '0', paddingLeft: '20px', '& li': { display: 'grid', gap: '2px' } });

export function ConsultView(handle: Handle<{ r: PackConsult }>) {
  return () => {
    const r = handle.props.r;
    return (
      <div mix={resultCss} data-testid="pack-consult" data-found={String(r.found)}>
        <p mix={headlineCss}>
          <Badge tone={r.confidence === 'high' ? 'ok' : r.confidence === 'none' ? 'muted' : 'warn'}>{r.confidence}</Badge>
          <span mix={faint}>
            route {r.route}
            {r.model ? `, model ${r.model}` : ''}; source age {r.sourceAgeDays} day(s)
          </span>
        </p>
        {r.degraded ? <Notice tone="warn">{r.degraded}</Notice> : null}
        <div mix={answerCss}>
          <Markdown source={r.answer || '(no answer)'} />
        </div>
        <h4>Citations</h4>
        <ol mix={sourcesCss}>
          {(r.sources ?? []).map((s) => (
            <li key={s.n}>
              <CitationLine c={s.citation} />
              <span mix={faint}>{s.excerpt}</span>
            </li>
          ))}
        </ol>
        <p mix={faint}>
          Cost of this question: {formatCost(r.usage.costUsd)} ({r.usage.inputTokens} in / {r.usage.outputTokens} out tokens). The answer is generated from the cited text;
          check the citations.
        </p>
      </div>
    );
  };
}

// --- quarantine ---

const quarCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  background: t.bgApp,
  border: `1px solid ${t.border}`,
});
const chunkCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid color-mix(in srgb, ${t.warning} 35%, ${t.border})`,
  background: t.bgSurface,
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontSize: t.textSm },
});

function QuarantineList(handle: Handle<{ packId: string; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.packQuarantined(handle.props.packId),
    key: () => handle.props.packId,
    when: (e) => e.type.startsWith('pack.'),
  });
  return () => {
    const rows: QuarantinedChunk[] = q.data ?? [];
    return (
      <div mix={quarCss} data-testid="quarantine">
        <p mix={faint}>These passages looked like instructions to an agent and were kept out of search. Only you can release one.</p>
        <ErrorNote error={q.error ?? act.error} />
        {q.data && rows.length === 0 ? <p mix={faint}>Nothing is quarantined.</p> : null}
        {rows.map((c) => (
          <div key={c.chunkId} mix={chunkCss} data-testid="quarantined-chunk">
            <div class="head">
              <Badge tone="warn">risk {c.risk}</Badge>
              <span>{(c.flags ?? []).join(', ')}</span>
              <span mix={muted}>{c.headingPath}</span>
            </div>
            <Markdown source={c.body} maxChars={3000} />
            <div>
              <Button
                size="sm"
                icon={ShieldAlert}
                disabled={act.busy !== ''}
                mix={on('click', async () => {
                  await act.run('release', () => s.client.packReleaseChunk(c.chunkId));
                  q.reload();
                  handle.props.onChanged();
                })}
              >
                Release this passage to search
              </Button>
            </div>
          </div>
        ))}
      </div>
    );
  };
}

// --- one pack ---

const packCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& header': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0' },
  '& h3': { fontSize: t.textLg, fontWeight: '600', overflowWrap: 'anywhere' },
  '& .attached': { fontSize: t.textMd },
});
const statsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))',
  gap: t.s3,
  margin: '0',
  padding: t.s3,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& div': { display: 'grid', gap: '2px', minWidth: '0' },
  '& dt': { color: t.textMuted, fontSize: t.textXs, fontWeight: '500', textTransform: 'uppercase', letterSpacing: '0.04em' },
  '& dd': { margin: '0', fontSize: t.textMd, overflowWrap: 'anywhere' },
});
const actionsCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& input': { width: '150px' } });
const packIcon = css({ display: 'inline-grid', placeItems: 'center', width: '28px', height: '28px', borderRadius: t.rMd, background: t.accentSoft, color: t.accentText });

function PackCard(handle: Handle<{ p: Pack; projectId: string; attached: PackAttachment[]; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let role = '';
  let showQ = false;
  let confirm = false;
  let refreshed: string | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  return () => {
    const { p, projectId, attached, onChanged } = handle.props;
    const mine = attached.filter((a) => a.packId === p.id);
    const busy = act.busy !== '';
    return (
      <article mix={packCss} data-testid="pack" data-pack={p.id}>
        <header>
          <span mix={packIcon}>
            <Icon icon={Library} size={15} />
          </span>
          <h3>{p.title}</h3>
          <code>{p.id}</code>
          <Badge>{p.kind}</Badge>
          <Badge tone={p.visibility === 'private' ? 'ok' : 'warn'}>{p.visibility}</Badge>
          <Badge tone={p.status === 'ready' ? 'ok' : 'muted'}>{p.status}</Badge>
          <span mix={spacer} />
          <Button
            size="sm"
            icon={RefreshCw}
            disabled={busy}
            mix={on('click', async () => {
              const r = await act.run('refresh', () => s.client.packRefresh(p.id, true));
              if (r) set(() => (refreshed = refreshText(r)));
              onChanged();
            })}
          >
            {act.busy === 'refresh' ? 'Refreshing...' : 'Refresh now'}
          </Button>
        </header>
        <dl mix={statsCss}>
          <div>
            <dt>Version</dt>
            <dd>{p.version || 'not fetched yet'}</dd>
          </div>
          <div>
            <dt>Content</dt>
            <dd>
              {p.pages} pages, {p.chunks} passages, {p.quarantined} quarantined
            </dd>
          </div>
          <div>
            <dt>Freshness</dt>
            <dd>{freshnessText(p)}</dd>
          </div>
          <div>
            <dt>Last refresh</dt>
            <dd>{zeroTime(p.lastSuccessAt) ? 'never' : formatDateTime(p.lastSuccessAt)}</dd>
          </div>
          {p.manifest?.license?.content ? (
            <div>
              <dt>License</dt>
              <dd>{p.manifest.license.content}</dd>
            </div>
          ) : null}
        </dl>
        {p.lastError ? <ErrorNote error={p.lastError} what="Last refresh failed" /> : null}
        {p.manifestDrift ? <Notice tone="warn">pack.yaml on disk differs from the installed manifest.</Notice> : null}
        <ErrorNote error={act.error} />
        {refreshed ? (
          <Notice tone="ok" role="status">
            {refreshed}
          </Notice>
        ) : null}
        <p class="attached">
          <span mix={muted}>Attached to this project:</span> {attachedText(mine)}
        </p>
        <div mix={actionsCss}>
          <input aria-label={`Role for ${p.id}`} value={role} placeholder="all roles" mix={on('input', (e) => set(() => (role = e.currentTarget.value)))} />
          <Button
            size="sm"
            disabled={busy}
            mix={on('click', async () => {
              await act.run('attach', () => s.client.packAttach(projectId, p.id, '', role.trim()));
              onChanged();
            })}
          >
            Attach to this project
          </Button>
          {mine.length > 0 ? (
            <Button
              size="sm"
              disabled={busy}
              mix={on('click', async () => {
                await act.run('detach', () => s.client.packDetach(projectId, p.id, mine[0]!.agentId ?? '', mine[0]!.role ?? ''));
                onChanged();
              })}
            >
              Detach
            </Button>
          ) : null}
          <Button size="sm" variant="ghost" icon={ShieldAlert} aria-expanded={showQ ? 'true' : 'false'} mix={on('click', () => set(() => (showQ = !showQ)))}>
            Quarantined ({p.quarantined})
          </Button>
          <span mix={spacer} />
          {confirm ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={busy}
                mix={on('click', async () => {
                  await act.run('remove', () => s.client.packRemove(p.id));
                  onChanged();
                })}
              >
                Yes, remove {p.id}
              </Button>
              <Button size="sm" variant="ghost" mix={on('click', () => set(() => (confirm = false)))}>
                Keep
              </Button>
            </>
          ) : (
            <Button size="sm" variant="ghost" icon={Trash2} mix={on('click', () => set(() => (confirm = true)))}>
              Remove
            </Button>
          )}
        </div>
        {showQ ? <QuarantineList packId={p.id} onChanged={onChanged} /> : null}
      </article>
    );
  };
}

// --- the screen ---

const formCss = css({ display: 'grid', gap: t.s3 });
const inlineForm = css({
  display: 'flex',
  alignItems: 'flex-end',
  gap: t.s2,
  flexWrap: 'wrap',
  '& > div': { flex: '1', minWidth: '240px' },
});
const choiceCss = css({ display: 'flex', flexWrap: 'wrap', gap: `${t.s2} ${t.s4}`, '& label': { margin: '0' } });
const yamlCss = css({ fontFamily: t.mono, fontSize: t.textSm });

export function PacksScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  const packs = query(handle, { load: () => s.client.packList(), when: (e) => e.type.startsWith('pack.') });
  const attachments = query(handle, {
    load: () => s.client.packAttachments(pid()!),
    when: (e) => e.type.startsWith('pack.'),
    key: pid,
    enabled: () => !!pid(),
  });
  let yaml = '';
  let search = '';
  let question = '';
  let selected: string[] = [];
  let result: PackSearchResult | undefined;
  let consult: PackConsult | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const reload = () => {
    packs.reload();
    attachments.reload();
  };

  return () => {
    const list = packs.data ?? [];
    const ids = packIds(selected, list);
    const busy = act.busy !== '';
    const projectId = pid() ?? '';
    return (
      <Page title="Expert packs" icon={BookOpen} subtitle={packs.data ? `${list.length} installed` : undefined}>
        <p mix={muted}>
          A pack is a body of reference text (documentation, standards) that agents search and cite. Everything inside a pack is outside text: it is shown here as inert text,
          passages that look like instructions are quarantined, and nothing in a pack can grant a permission or make a decision.
        </p>
        <ErrorNote error={packs.error ?? act.error} what="Packs" />
        {packs.loading && !packs.data ? <Loading /> : null}
        {packs.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="No packs installed." icon={BookOpen}>
              <p>Install a pack from its manifest below, then refresh it to fetch the text.</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <div mix={stack}>
            {list.map((p) => (
              <PackCard key={p.id} p={p} projectId={projectId} attached={attachments.data ?? []} onChanged={reload} />
            ))}
          </div>
        ) : null}

        <Section title="Search and ask" description="Search returns passages with citations; a question is answered from the cited text by a model.">
          <div mix={[card, formCss]}>
            {list.length > 0 ? (
              <fieldset>
                <legend>Packs to use (none selected means all)</legend>
                <div mix={choiceCss}>
                  {list.map((p) => (
                    <Checkbox
                      key={p.id}
                      label={p.id}
                      checked={selected.includes(p.id)}
                      onChange={(v) => set(() => (selected = v ? [...selected, p.id] : selected.filter((x) => x !== p.id)))}
                    />
                  ))}
                </div>
              </fieldset>
            ) : null}
            <form
              mix={[
                inlineForm,
                on('submit', async (e) => {
                  e.preventDefault();
                  if (busy || !search.trim() || ids.length === 0) return;
                  const r = await act.run('search', () => s.client.packSearch(ids, search.trim(), 8));
                  set(() => (result = r));
                }),
              ]}
            >
              <Field label="Search">
                {(id) => <input id={id} type="search" aria-label="Search packs" value={search} mix={on('input', (e) => set(() => (search = e.currentTarget.value)))} />}
              </Field>
              <Button type="submit" icon={Search} disabled={busy || !search.trim() || ids.length === 0}>
                Search
              </Button>
            </form>
            {result ? <SearchResultView r={result} /> : null}
            <form
              mix={[
                inlineForm,
                on('submit', async (e) => {
                  e.preventDefault();
                  if (busy || !question.trim() || ids.length === 0) return;
                  const r = await act.run('consult', () => s.client.packConsult(ids, question.trim()));
                  set(() => (consult = r));
                }),
              ]}
            >
              <Field label="Ask a question (uses a model call and may cost a little)">
                {(id) => <input id={id} aria-label="Ask the packs" value={question} mix={on('input', (e) => set(() => (question = e.currentTarget.value)))} />}
              </Field>
              <Button type="submit" icon={MessageSquareText} disabled={busy || !question.trim() || ids.length === 0}>
                {act.busy === 'consult' ? 'Consulting...' : 'Consult'}
              </Button>
            </form>
            {consult ? <ConsultView r={consult} /> : null}
          </div>
        </Section>

        <Section
          title="Install a pack"
          description="Paste a pack manifest (YAML). A source can be a local folder (type local_dir with a path), a list of URLs or a site. Installing does not fetch anything; use Refresh."
        >
          <form
            mix={[
              card,
              formCss,
              on('submit', async (e) => {
                e.preventDefault();
                if (busy || !yaml.trim()) return;
                const r = await act.run('install', () => s.client.packInstall(yaml));
                if (r) {
                  set(() => (yaml = ''));
                  reload();
                }
              }),
            ]}
          >
            <Field label="Pack manifest (YAML)">
              {(id) => (
                <textarea
                  id={id}
                  rows={8}
                  spellcheck="false"
                  placeholder={'schema: 1\nid: godocs\ntitle: Go docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: "C:/docs"}'}
                  value={yaml}
                  mix={[yamlCss, on('input', (e) => set(() => (yaml = e.currentTarget.value)))]}
                />
              )}
            </Field>
            <div mix={formActions}>
              <Button type="submit" variant="primary" icon={Download} disabled={busy || !yaml.trim()}>
                Install pack
              </Button>
            </div>
          </form>
        </Section>
      </Page>
    );
  };
}
