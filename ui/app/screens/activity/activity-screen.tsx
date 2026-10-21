// Activity: the event log as the core persisted it, newest first, filtered by type prefix and (by default) to this project.
// The stream starts at the head, so older events are loaded here on demand (the most recent ~1000 sequence numbers).

import { css, on, type Handle } from 'remix/component';
import { Activity, ListFilter } from 'lucide';
import type { ConnectionState } from '../../state/eventStore.ts';
import { formatTime } from '../../lib/format.ts';
import { services, watch } from '../../state/services.tsx';
import { Badge, mono } from '../../ui/data.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, tableCard } from '../../ui/layout.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { selectRows, typeTone } from './activity.ts';

/** An id's start, so a column of them stays readable; the full id is its tooltip. */
const shortId = (id: string): string => (id.length > 14 ? `${id.slice(0, 11)}…` : id);

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

const tableCss = css({
  '& td': { fontSize: t.textSm },
  '& td.seq': { color: t.textFaint, fontVariantNumeric: 'tabular-nums', width: '1%', whiteSpace: 'nowrap' },
  '& td.time': { color: t.textMuted, whiteSpace: 'nowrap', width: '1%', fontVariantNumeric: 'tabular-nums' },
  '& td.type': { whiteSpace: 'nowrap' },
  '& td.type code': { display: 'inline-flex', alignItems: 'center', gap: '6px' },
  // one id per line, never broken inside: a squeezed column broke them a character per line (task_ / 945cb / a6343 ...)
  '& td.scope': { color: t.textMuted, fontFamily: t.mono, fontSize: t.textXs, width: '1%' },
  '& td.scope > span': { display: 'block', whiteSpace: 'nowrap' },
  '& td.data': { color: t.textMuted, maxWidth: '0', width: '50%', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& td.data .peek': { display: 'block', width: '100%', minHeight: '28px', lineHeight: '28px', padding: '0', border: '0', background: 'transparent', color: 'inherit', textAlign: 'left', cursor: 'pointer', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& td.data .peek:hover': { color: t.text },
  '& tr.detail td': { background: t.bgRaised },
  '& tr.detail pre': { margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm },
  '& tr.detail p': { margin: `${t.s2} 0 0`, color: t.textMuted },
});

const dot = css({ display: 'inline-block', width: '6px', height: '6px', borderRadius: '50%', flex: 'none' });

export function ActivityScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const open = new Set<number>();
  let filter = '';
  let thisProject = true;
  let historyError: Error | undefined;

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

  return () => {
    const snap = s.store.getSnapshot();
    const rows = selectRows(snap.events, { filter, projectId: thisProject ? s.workspace.projectId : undefined });
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
        <div mix={toolbarCss}>
          <div class="filter">
            <label for={`filter-${handle.id}`}>Filter by type prefix</label>
            <span class="box">
              <Icon icon={ListFilter} size={14} />
              <input
                id={`filter-${handle.id}`}
                value={filter}
                placeholder="task.*, agent.*"
                spellcheck={false}
                autocomplete="off"
                mix={on('input', (e) => ((filter = e.currentTarget.value), void handle.update()))}
              />
            </span>
          </div>
          <Checkbox label="This project only" checked={thisProject} onChange={(v) => ((thisProject = v), void handle.update())} />
          <span class="count">
            {rows.length} {rows.length === 1 ? 'event' : 'events'} shown
          </span>
        </div>
        {rows.length === 0 ? (
          <Empty title="No matching events" icon={Activity}>
            <p>Events appear here as the core records them. Clear the filter to see everything.</p>
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
                      <td class="scope">
                        {[e.taskId, e.agentId, e.runId].filter(Boolean).map((id) => (
                          <span key={id} title={id}>
                            {shortId(id!)}
                          </span>
                        ))}
                      </td>
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
                    </tr>,
                    open.has(e.seq) ? (
                      <tr key={`${e.seq}-data`} class="detail" data-testid="event-detail">
                        <td colSpan={5}>
                          <pre mix={mono}>{e.preview}</pre>
                          {e.cut ? <p>Cut here; the core has the rest.</p> : null}
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
