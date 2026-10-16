// Project memory: decisions, conventions and known issues agents read. Agents (and the human) propose entries; only an entry
// the human accepts reaches agents. Entry text is outside text and is rendered inert.
//
// An entry's text is shown whole, as it was written, in a box of a most height that scrolls and can be reached by keyboard, with its
// length beside it (audit 2026-10-16, M26). It is not drawn as Markdown: the Markdown view shows the end of a long paragraph behind
// a button and leaves out link definitions, so what the person read would not be all the text that becomes the project's memory.

import { css, on, type Handle } from 'remix/component';
import { Brain, Check, Search, Trash2, X } from 'lucide';
import type { MemoryEntry } from '../../api/extTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, spacer } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { visible } from '../../lib/visible.ts';
import { bodyNote, kindLabel, MEMORY_FILTERS, MEMORY_KINDS, memoryMeta, memoryTone } from './memory.ts';

const listCss = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
});
const entryCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  '&:first-child': { borderTop: '0' },
  '& header': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
    minWidth: '0',
  },
  '& h3': { fontSize: t.textMd, fontWeight: '600', overflowWrap: 'anywhere' },
  '& .meta': { color: t.textMuted, fontSize: t.textSm },
  '& .body': { fontSize: t.textMd },
  '& .actions': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
  },
  '&[data-status="proposed"]': { boxShadow: `inset 2px 0 0 ${t.warning}` },
  '&[data-status="rejected"] .body': { color: t.textMuted },
});

// the text of an entry: all of it, in a box that stops growing at a height and scrolls
const bodyBox = css({
  display: 'block',
  margin: '0',
  padding: `${t.s2} ${t.s3}`,
  maxHeight: '320px',
  overflow: 'auto',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  fontFamily: t.mono,
  fontSize: t.textSm,
  lineHeight: '1.55',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  '&:focus-visible': { outline: 'none', boxShadow: t.focus },
});

/** One memory entry with its decision buttons. Delete asks first. */
function MemoryCard(
  handle: Handle<{
    e: MemoryEntry;
    busy: boolean;
    onAccept: () => void;
    onReject: () => void;
    onDelete: () => void;
  }>,
) {
  let confirm = false;
  const setConfirm = (v: boolean) => {
    confirm = v;
    void handle.update();
  };
  return () => {
    const { e, busy, onAccept, onReject, onDelete } = handle.props;
    // the text comes from the core: a body that is not text is shown as no text rather than blanking the page
    const body = typeof e.body === 'string' ? e.body : '';
    const note = bodyNote(body);
    return (
      <li mix={entryCss} data-testid="memory" data-status={e.status} data-title={e.title}>
        <header>
          <h3>{e.title}</h3>
          <Badge tone="accent">{kindLabel(e.kind)}</Badge>
          <Badge tone={memoryTone(e.status)}>{e.status}</Badge>
        </header>
        <p class="meta">{memoryMeta(e, formatDateTime(e.updated))}</p>
        <div class="body">
          <pre mix={bodyBox} role="group" aria-label={`Full text of the ${e.status === 'proposed' ? 'proposal' : 'entry'}`} tabIndex={0} data-testid="memory-body">
            {visible(body)}
          </pre>
          <p class="meta" data-testid="memory-chars" data-over={String(note.over)}>
            {note.text}
          </p>
        </div>
        <div class="actions">
          {e.status === 'proposed' ? (
            <>
              <Button variant="primary" size="sm" icon={Check} disabled={busy} mix={on('click', onAccept)}>
                Accept
              </Button>
              <Button size="sm" icon={X} disabled={busy} mix={on('click', onReject)}>
                Reject
              </Button>
            </>
          ) : null}
          {e.status === 'rejected' ? (
            <Button size="sm" disabled={busy} mix={on('click', onAccept)}>
              Accept after all
            </Button>
          ) : null}
          <span mix={spacer} />
          {confirm ? (
            <>
              <Button variant="danger" size="sm" disabled={busy} mix={on('click', onDelete)}>
                Yes, delete
              </Button>
              <Button variant="ghost" size="sm" mix={on('click', () => setConfirm(false))}>
                Keep
              </Button>
            </>
          ) : (
            <Button variant="ghost" size="sm" icon={Trash2} mix={on('click', () => setConfirm(true))}>
              Delete
            </Button>
          )}
        </div>
      </li>
    );
  };
}

const toolbar = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
});
const segmented = css({
  display: 'inline-flex',
  padding: '2px',
  gap: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  '& button': {
    height: '26px',
    minHeight: '26px',
    padding: '0 10px',
    border: '0',
    borderRadius: t.rSm,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '500',
    textTransform: 'capitalize',
  },
  '& button:hover': { color: t.text, background: t.bgHover },
  '& button[aria-pressed="true"]': { color: t.text, background: t.bgActive },
});
const searchForm = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flex: '1',
  minWidth: '260px',
  '& .box': { position: 'relative', flex: '1', minWidth: '0' },
  '& .box svg': {
    position: 'absolute',
    left: '9px',
    top: '50%',
    transform: 'translateY(-50%)',
    color: t.textFaint,
    pointerEvents: 'none',
  },
  '& .box input': { paddingLeft: '30px' },
});
const formCss = css({ display: 'grid', gap: t.s4 });

export function MemoryScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  let status = '';
  let search = '';
  let found: MemoryEntry[] | undefined;
  let kind = 'convention';
  let title = '';
  let body = '';
  const q = query(handle, {
    load: () => s.client.memoryList(pid()!, status),
    when: (e) => e.type.startsWith('memory.'),
    key: () => [pid(), status],
    enabled: () => !!pid(),
  });

  const decide = (fn: (id: string) => Promise<unknown>, id: string) => async () => {
    await act.run(id, () => fn(id));
    q.reload();
    found = undefined;
    void handle.update();
  };
  const runSearch = async () => {
    const projectId = pid();
    if (!projectId || !search.trim()) return;
    const r = await act.run('search', () => s.client.memorySearch(projectId, search.trim(), status));
    if (r) {
      found = r;
      void handle.update();
    }
  };
  const propose = async () => {
    const projectId = pid();
    if (!projectId) return;
    const r = await act.run('propose', () => s.client.memoryPropose(projectId, kind, title.trim(), body));
    if (r) {
      title = '';
      body = '';
      q.reload();
      void handle.update();
    }
  };

  return () => {
    const list = found ?? q.data ?? [];
    const counted = q.data && !found ? `${q.data.length} ${status ? (MEMORY_FILTERS.find((f) => f.value === status)?.label ?? status) : 'entries'}` : undefined;
    return (
      <Page title="Memory" icon={Brain} subtitle={found ? `${found.length} found` : counted}>
        <p mix={muted}>
          Project memory: decisions, conventions and known issues agents read. Agents (and you) can propose entries; only an entry you accept reaches agents.
          Secrets are redacted before anything is stored.
        </p>
        <div mix={toolbar}>
          <div mix={segmented} role="group" aria-label="Show">
            {MEMORY_FILTERS.map((f) => (
              <button
                key={f.value}
                type="button"
                aria-pressed={status === f.value ? 'true' : 'false'}
                mix={on('click', () => {
                  status = f.value;
                  found = undefined;
                  void handle.update();
                })}
              >
                {f.label}
              </button>
            ))}
          </div>
          <form
            mix={[
              searchForm,
              on('submit', (e) => {
                e.preventDefault();
                void runSearch();
              }),
            ]}
          >
            <span class="box">
              <Icon icon={Search} size={14} />
              <input
                type="search"
                aria-label="Search memory"
                placeholder="Search titles and text"
                value={search}
                mix={on('input', (e) => ((search = e.currentTarget.value), void handle.update()))}
              />
            </span>
            <Button type="submit" disabled={!search.trim()}>
              Search
            </Button>
            {found ? (
              <Button
                variant="ghost"
                mix={on('click', () => {
                  found = undefined;
                  search = '';
                  void handle.update();
                })}
              >
                Clear search
              </Button>
            ) : null}
          </form>
        </div>
        <ErrorNote error={q.error ?? act.error} what="Memory" />
        {q.loading && !q.data && !found ? <Loading /> : null}
        {(q.data || found) && list.length === 0 ? (
          <div mix={card}>
            <Empty title={found ? 'Nothing matches.' : 'Nothing here.'} icon={found ? Search : Brain}>
              <p>{found ? 'Try other words, or clear the search.' : 'Entries agents propose, and the ones you add below, appear here.'}</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <ul mix={listCss} aria-label="Memory entries">
            {list.map((e) => (
              <MemoryCard
                key={e.id}
                e={e}
                busy={act.busy !== ''}
                onAccept={decide(s.client.memoryAccept, e.id)}
                onReject={decide(s.client.memoryReject, e.id)}
                onDelete={decide(s.client.memoryDelete, e.id)}
              />
            ))}
          </ul>
        ) : null}
        <Section title="Propose an entry" description="An entry you write starts as a proposal too: accept it to make it visible to agents.">
          <form
            mix={[
              card,
              formCss,
              on('submit', (e) => {
                e.preventDefault();
                if (!act.busy && title.trim() && body.trim()) void propose();
              }),
            ]}
          >
            <div mix={formGrid}>
              <Field label="Kind">
                {(id) => (
                  <Select id={id} label="Kind" value={kind} options={selectValues(MEMORY_KINDS)} onChange={(v) => ((kind = v), void handle.update())} />
                )}
              </Field>
              <Field label="Title">
                {(id) => <input id={id} value={title} mix={on('input', (e) => ((title = e.currentTarget.value), void handle.update()))} />}
              </Field>
            </div>
            <Field label="Body">
              {(id) => <textarea id={id} rows={4} value={body} mix={on('input', (e) => ((body = e.currentTarget.value), void handle.update()))} />}
            </Field>
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={act.busy !== '' || !title.trim() || !body.trim()}>
                Propose
              </Button>
            </div>
          </form>
        </Section>
      </Page>
    );
  };
}
