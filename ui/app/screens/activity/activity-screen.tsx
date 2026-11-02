// Activity: the event log as the core persisted it, newest first, filtered by family chips, a type prefix, one task, run or
// agent, and (by default) to this project. The filter is remembered per project (localStorage, a convenience only); the run
// page links here with ?run=<id>&task=<id>, which sets the scope (once, for the project open when the page is) and is remembered
// the same way. The stream starts at the head, so older events are loaded here on demand (the most recent ~1000 sequence
// numbers); a task or run scope also reads that task's events from the core (events.replay by task, page by page), since a run
// from yesterday is long gone from the recent ones. A cut payload is fetched whole from the core (events.replay of that one seq)
// when the person asks.

import { css, on, type Handle } from 'remix/component';
import { Activity, Copy, ListFilter, X } from 'lucide';
import { rowOf, type ConnectionState, type EventRow } from '../../state/eventStore.ts';
import { formatTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, mono } from '../../ui/data.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, tableCard } from '../../ui/layout.tsx';
import { MoreMenu, type MenuItem } from '../../ui/menu.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { FAMILIES, filterRows, mergeRows, readFilter, typeTone, writeFilter, type ActivityFilter, type Family, type Scope } from './activity.ts';

/** An id's start, so a column of them stays readable; the full id is its tooltip. */
const shortId = (id: string): string => (id.length > 14 ? `${id.slice(0, 11)}…` : id);

/** At most this many rows are listed (the newest that match). */
const ROW_LIMIT = 500;
/** A scope's task is read from the core in pages of this many events, at most this many pages; the newest SCOPE_KEEP that match are kept. */
const SCOPE_PAGE = 1000;
const SCOPE_PAGES = 20;
const SCOPE_KEEP = 5000;

const STATUS_TONE: Record<ConnectionState, Tone> = { open: 'ok', connecting: 'info', reconnecting: 'warn', idle: 'muted', stopped: 'err' };


const toolbarCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s4,
  '& .filter': { display: 'flex', alignItems: 'center', gap: t.s2, flex: '1', minWidth: '260px', maxWidth: '520px' },
  '& .filter label': { fontSize: t.textSm, fontWeight: '500', whiteSpace: 'nowrap' },
  '& .box': { position: 'relative', flex: '1', minWidth: '0' },
  '& .box svg': { position: 'absolute', left: '9px', top: '50%', transform: 'translateY(-50%)', color: t.textFaint, pointerEvents: 'none' },
  '& .box input': { width: '100%', paddingLeft: '30px', fontFamily: t.mono, fontSize: t.textSm },
  '& .count': { marginLeft: 'auto', color: t.textMuted, fontSize: t.textSm },
});

const chipsCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s2,
  '& button.chip': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    minHeight: '28px',
    padding: `0 ${t.s3}`,
    borderRadius: t.rLg,
    border: `1px solid ${t.border}`,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    cursor: 'pointer',
  },
  '& button.chip:hover': { color: t.text, background: t.bgHover },
  '& button.chip[aria-pressed="true"]': { color: t.text, background: t.bgActive, borderColor: t.borderStrong },
  '& .scope': { display: 'inline-flex', alignItems: 'center', gap: t.s1, fontSize: t.textSm },
});

const tableCss = css({
  '& td': { fontSize: t.textSm },
  '& td.seq': { color: t.textFaint, fontVariantNumeric: 'tabular-nums', width: '1%', whiteSpace: 'nowrap' },
  '& td.time': { color: t.textMuted, whiteSpace: 'nowrap', width: '1%', fontVariantNumeric: 'tabular-nums' },
  '& td.type': { whiteSpace: 'nowrap' },
  '& td.type code': { display: 'inline-flex', alignItems: 'center', gap: '6px' },
  // one id per line, never broken inside: a squeezed column broke them a character per line (task_ / 945cb / a6343 ...)
  '& td.scope': { color: t.textMuted, fontFamily: t.mono, fontSize: t.textXs, width: '1%' },
  '& td.scope > span, & td.scope > a': { display: 'block', whiteSpace: 'nowrap' },
  '& td.more': { width: '1%' },
  '& td.data': { color: t.textMuted, maxWidth: '0', width: '50%', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& td.data .peek': { display: 'block', width: '100%', minHeight: '28px', lineHeight: '28px', padding: '0', border: '0', background: 'transparent', color: 'inherit', textAlign: 'left', cursor: 'pointer', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& td.data .peek:hover': { color: t.text },
  '& tr.detail td': { background: t.bgRaised },
  '& tr.detail pre': { margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm },
  '& tr.detail p': { margin: `${t.s2} 0 0`, color: t.textMuted, display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
});

const noteCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });

const dot = css({ display: 'inline-block', width: '6px', height: '6px', borderRadius: '50%', flex: 'none' });

/** `task`, `run` and `agent` come from the address (/activity?run=<id>&task=<id>): they set the scope, which is then remembered. */
export function ActivityScreen(handle: Handle<{ task?: string; run?: string; agent?: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const open = new Set<number>();
  let f: ActivityFilter = readFilter(undefined);
  /** The project the filter was read for: a project chosen later (or loaded after the first render) reads its own. */
  let filterFor: string | undefined | null = null;
  let historyError: Error | undefined;
  /** The payloads fetched whole for cut rows (by seq), and the one being fetched. */
  const whole = new Map<number, string>();
  let fetching = -1;
  /** A failed fetch is said under its own row only. */
  const fetchErrors = new Map<number, Error>();
  let copied = -1;
  /** The scope from the address is applied once, to the project open when the page is: never to a project chosen later. */
  let urlUsed = false;
  /** The scoped task's events (only its run's, when a run is scoped) as read from the core for the scope `scopeKey`. */
  let scopeKey = '';
  let scopeRows: EventRow[] = [];
  let scopeLoading = false;
  let scopeError: Error | undefined;
  /** The task has more events than the page reads, or the run more than it keeps: what is listed is not all of them. */
  let scopePartial = false;

  const fromUrl = (): Scope | undefined => {
    const { task, run, agent } = handle.props;
    if (!task && !run && !agent) return undefined;
    const sc: Scope = {};
    if (task) sc.taskId = task;
    if (run) sc.runId = run;
    if (agent) sc.agentId = agent;
    return sc;
  };
  const sync = () => {
    const pid = s.workspace.projectId;
    if (filterFor === pid) return;
    filterFor = pid;
    f = readFilter(pid);
    if (urlUsed || pid === undefined) return;
    urlUsed = true;
    const sc = fromUrl();
    if (sc) {
      f = { ...f, scope: sc };
      writeFilter(pid, f);
    }
  };
  /** Read the scoped task's events from the core (oldest first, page by page) when the scope changed. */
  const loadScope = () => {
    const sc = f.scope;
    const key = sc.taskId ? `${sc.taskId}/${sc.runId ?? ''}` : '';
    if (key === scopeKey) return;
    scopeKey = key;
    scopeRows = [];
    scopeError = undefined;
    scopePartial = false;
    scopeLoading = !!key;
    if (!key) return;
    const taskId = sc.taskId!;
    const runId = sc.runId;
    void (async () => {
      const kept: EventRow[] = [];
      let partial = false;
      try {
        let after = 0;
        for (let page = 1; ; page++) {
          const evs = await s.client.eventsReplay(after, { taskId, limit: SCOPE_PAGE });
          if (handle.signal.aborted || scopeKey !== key) return;
          for (const e of evs) if (typeof e.seq === 'number' && (!runId || e.runId === runId)) kept.push(rowOf(e));
          if (kept.length > SCOPE_KEEP) {
            kept.splice(0, kept.length - SCOPE_KEEP);
            partial = true;
          }
          if (evs.length < SCOPE_PAGE) break;
          after = evs[evs.length - 1]!.seq;
          if (page >= SCOPE_PAGES) {
            partial = true;
            break;
          }
        }
        scopeRows = kept;
        scopePartial = partial;
      } catch (e) {
        if (handle.signal.aborted || scopeKey !== key) return;
        scopeError = e instanceof Error ? e : new Error(String(e));
      }
      scopeLoading = false;
      void handle.update();
    })();
  };
  const change = (next: Partial<ActivityFilter>) => {
    f = { ...f, ...next };
    writeFilter(filterFor ?? undefined, f);
    void handle.update();
  };
  const toggleFamily = (id: Family) => change({ families: f.families.includes(id) ? f.families.filter((x) => x !== id) : [...f.families, id] });

  const showWhole = async (seq: number) => {
    fetching = seq;
    fetchErrors.delete(seq);
    void handle.update();
    try {
      const [e] = await s.client.eventsReplay(seq - 1, { limit: 1 });
      if (handle.signal.aborted) return;
      if (!e || e.seq !== seq) throw new Error(`the core no longer has event ${seq}`);
      whole.set(seq, JSON.stringify(e.data ?? null, null, 2));
    } catch (err) {
      fetchErrors.set(seq, err instanceof Error ? err : new Error(String(err)));
    }
    fetching = -1;
    if (!handle.signal.aborted) void handle.update();
  };
  const copyWhole = async (seq: number) => {
    try {
      await navigator.clipboard.writeText(whole.get(seq) ?? '');
      copied = seq;
      void handle.update();
    } catch {
      /* the clipboard can be refused; the payload is on the page to select */
    }
  };

  // re-render on new events, at most every 50ms (a busy agent streams many)
  let pending = false;
  const off = s.store.subscribe(() => {
    if (pending) return;
    pending = true;
    setTimeout(() => {
      pending = false;
      if (!handle.signal.aborted) void handle.update();
    }, 50);
  });
  handle.signal.addEventListener('abort', () => void off());

  const from = Math.max(0, s.store.getSnapshot().lastSeq - 1000);
  Promise.resolve()
    .then(() => s.client.eventsReplay(from, { limit: 1000 }))
    .then((evs) => {
      if (!handle.signal.aborted) s.store.backfill(evs);
    })
    .catch((e: unknown) => {
      if (handle.signal.aborted) return;
      historyError = e instanceof Error ? e : new Error(String(e));
      void handle.update();
    });

  /** The task's key (GOC-41) when the project's task list has it, else the id's start. */
  const taskName = (id: string): string => {
    const task = s.project.taskList.find((x) => x.id === id);
    const key = s.workspace.project?.key;
    return task && key ? `${key}-${task.seq}` : task ? `#${task.seq}` : shortId(id);
  };
  const agentName = (id: string): string => s.project.agentList.find((a) => a.id === id)?.name || shortId(id);
  const scopeText = (sc: Scope): string =>
    sc.runId ? `run ${shortId(sc.runId)}${sc.taskId ? ` of ${taskName(sc.taskId)}` : ''}` : sc.taskId ? `task ${taskName(sc.taskId)}` : sc.agentId ? `agent ${agentName(sc.agentId)}` : '';

  const scopeCell = (e: EventRow) => (
    <>
      {e.taskId ? (
        <a key="t" href={routes.task.href({ id: e.taskId })} title={e.taskId}>
          {taskName(e.taskId)}
        </a>
      ) : null}
      {e.agentId ? (
        <a key="a" href={routes.agent.href({ id: e.agentId })} title={e.agentId}>
          {agentName(e.agentId)}
        </a>
      ) : null}
      {e.runId ? (
        e.taskId ? (
          <a key="r" href={routes.run.href({ id: e.taskId, runId: e.runId })} title={e.runId}>
            {shortId(e.runId)}
          </a>
        ) : (
          <span key="r" title={e.runId}>
            {shortId(e.runId)}
          </span>
        )
      ) : null}
    </>
  );
  const rowMenu = (e: EventRow): MenuItem[] => [
    ...(e.taskId ? [{ name: 'task', label: 'Only this task' }] : []),
    ...(e.runId ? [{ name: 'run', label: 'Only this run' }] : []),
    ...(e.agentId ? [{ name: 'agent', label: 'Only this agent' }] : []),
  ];
  const narrow = (e: EventRow, name: string) => {
    const sc: Scope = name === 'task' ? { taskId: e.taskId } : name === 'run' ? { runId: e.runId, ...(e.taskId ? { taskId: e.taskId } : {}) } : { agentId: e.agentId };
    change({ scope: sc });
  };

  return () => {
    sync();
    loadScope();
    const snap = s.store.getSnapshot();
    const held = scopeRows.length > 0 ? mergeRows(snap.events, scopeRows) : snap.events;
    const rows = filterRows(held, f, s.workspace.projectId, ROW_LIMIT);
    const scoped = scopeText(f.scope);
    /** The scope's task was read from the core (not only the recent events the page holds). */
    const readForScope = !!f.scope.taskId;
    return (
      <Page
        title="Activity"
        icon={Activity}
        guide="events"
        wide
        subtitle={
          <>
            Stream <Badge tone={STATUS_TONE[snap.status] ?? 'muted'}>{snap.status}</Badge>, last seq {snap.lastSeq}, {snap.events.length} events held
          </>
        }
      >
        <ErrorNote error={historyError} what="Could not load earlier events" />
        <ErrorNote error={scopeError} what={`Could not load the events of ${scoped}`} />
        <div mix={toolbarCss}>
          <div class="filter">
            <label for={`filter-${handle.id}`}>Filter by type prefix</label>
            <span class="box">
              <Icon icon={ListFilter} size={14} />
              <input
                id={`filter-${handle.id}`}
                value={f.text}
                placeholder="task.*, agent.*"
                spellcheck={false}
                autocomplete="off"
                mix={on('input', (e) => change({ text: e.currentTarget.value }))}
              />
            </span>
          </div>
          <Checkbox label="This project only" checked={f.thisProject} onChange={(v) => change({ thisProject: v })} />
          <span class="count">
            {rows.length} {rows.length === 1 ? 'event' : 'events'} shown
          </span>
        </div>
        <div mix={chipsCss} role="group" aria-label="Show" data-testid="activity-chips">
          {FAMILIES.map((x) => (
            <button key={x.id} type="button" class="chip" data-family={x.id} aria-pressed={f.families.includes(x.id) ? 'true' : 'false'} mix={on('click', () => toggleFamily(x.id))}>
              {x.label}
            </button>
          ))}
          {f.families.length > 0 ? (
            <button type="button" class="chip" data-family="" mix={on('click', () => change({ families: [] }))}>
              Show all
            </button>
          ) : null}
          {scoped ? (
            <span class="scope" data-testid="activity-scope">
              Only {scoped}
              <Button size="sm" variant="ghost" icon={X} aria-label={`Stop showing only ${scoped}`} mix={on('click', () => change({ scope: {} }))}>
                Clear
              </Button>
            </span>
          ) : null}
        </div>
        {scoped && scopeLoading ? (
          <p mix={noteCss} data-testid="activity-scope-loading">
            Loading the events of {scoped}...
          </p>
        ) : null}
        {scoped && scopePartial && !scopeLoading ? (
          <p mix={noteCss} data-testid="activity-scope-partial">
            {f.scope.runId ? 'This run' : 'This task'} has more events than this page reads, so not all of them are listed.
          </p>
        ) : null}
        {rows.length >= ROW_LIMIT ? (
          <p mix={noteCss} data-testid="activity-row-limit">
            Only the latest {ROW_LIMIT} matching events are listed.
          </p>
        ) : null}
        {rows.length === 0 ? (
          <Empty title="No matching events" icon={Activity}>
            {!scoped ? (
              <p>Events appear here as the core records them. Clear the filter to see everything.</p>
            ) : scopeLoading ? (
              <p>Still reading the events of {scoped} from the core.</p>
            ) : readForScope && !scopeError ? (
              <p data-testid="activity-scope-empty">None of the events the core has for {scoped} match the filter.</p>
            ) : (
              <p data-testid="activity-scope-empty">This page holds only recent events, and none of them are in this scope. Clear the scope to see everything.</p>
            )}
          </Empty>
        ) : (
          <div mix={[tableCard, tableCss]}>
            <table aria-label="Events">
              <thead>
                <tr>
                  <th scope="col">Seq</th>
                  <th scope="col">Time</th>
                  <th scope="col">Type</th>
                  <th scope="col">Scope</th>
                  <th scope="col">Data</th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.flatMap((e) => {
                  // the ring holds a preview of the payload (EVENT_PREVIEW_CHARS), not the payload: the row shows its start
                  const data = e.preview.slice(0, 200);
                  return [
                    <tr key={e.seq} data-testid="event-row" data-type={e.type}>
                      <td class="seq">{e.seq}</td>
                      <td class="time">{formatTime(e.time)}</td>
                      <td class="type">
                        <code>
                          <span mix={dot} aria-hidden="true" style={{ background: tone[typeTone(e.type)].fg }} />
                          {e.type}
                        </code>
                      </td>
                      <td class="scope">{scopeCell(e)}</td>
                      <td class="data">
                        {e.preview ? (
                          <button
                            type="button"
                            class="peek"
                            aria-expanded={open.has(e.seq) ? 'true' : 'false'}
                            aria-label={`${open.has(e.seq) ? 'Hide' : 'Show'} the data of event ${e.seq}`}
                            mix={on('click', () => (open.has(e.seq) ? open.delete(e.seq) : open.add(e.seq), void handle.update()))}
                          >
                            <span mix={mono}>{data}</span>
                          </button>
                        ) : null}
                      </td>
                      <td class="more">
                        {rowMenu(e).length > 0 ? <MoreMenu label={`More for event ${e.seq}`} items={rowMenu(e)} onSelect={(name) => narrow(e, name)} testId="event-more" /> : null}
                      </td>
                    </tr>,
                    open.has(e.seq) ? (
                      <tr key={`${e.seq}-data`} class="detail" data-testid="event-detail">
                        <td colSpan={6}>
                          <pre mix={mono}>{whole.get(e.seq) ?? e.preview}</pre>
                          {e.cut && !whole.has(e.seq) ? (
                            <p>
                              Cut here; the core has the rest.
                              <Button size="sm" disabled={fetching !== -1} mix={on('click', () => void showWhole(e.seq))}>
                                {fetching === e.seq ? 'Loading...' : 'Show whole payload'}
                              </Button>
                            </p>
                          ) : null}
                          {whole.has(e.seq) ? (
                            <p>
                              <Button size="sm" icon={Copy} mix={on('click', () => void copyWhole(e.seq))}>
                                {copied === e.seq ? 'Copied' : 'Copy as JSON'}
                              </Button>
                            </p>
                          ) : null}
                          {e.cut && !whole.has(e.seq) ? <ErrorNote error={fetchErrors.get(e.seq)} what="Could not load the whole payload" /> : null}
                        </td>
                      </tr>
                    ) : null,
                  ];
                })}
              </tbody>
            </table>
          </div>
        )}
      </Page>
    );
  };
}
