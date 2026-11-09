// A room's minutes (room-protocol s8; X19): the Markdown snapshots the core keeps, listed with their verify ticks and Download.
// A task's room keeps its minutes by rule when it closes; anyone can make them now. Each is an immutable row with its hash, so it
// can be checked against the messages it covers. Decisions are listed in #decisions (decisions.tsx, X110), not exported here.

import { css, on, type Handle } from 'remix/component';
import { CircleCheck, CircleX, Download, FileDown, ScrollText } from 'lucide';
import type { RoomExportView } from '../../api/runClient.ts';
import type { Room } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { exportFileName } from './messages.ts';

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd });
const choices = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2 });
const listCss = css({
  display: 'grid',
  gap: '2px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, minHeight: '32px', fontSize: t.textSm },
  '& .when': { fontFamily: t.mono },
  '& .who': { color: t.textMuted },
  '& .ok': { display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.success },
  '& .bad': { display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.danger },
});
const resultCss = css({
  display: 'grid',
  gap: t.s2,
  '& pre': { maxHeight: '50vh', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
});

/** The scope of the minutes a task's room keeps when it closes (room.TaskArchiveScope). */
export const TASK_ARCHIVE_SCOPE = 'system:task-archive';

/** Who made a snapshot, in words. */
export const madeBy = (ex: Pick<RoomExportView, 'scope'>): string => (ex.scope === TASK_ARCHIVE_SCOPE ? 'kept when the task closed' : 'made on request');

/** Download links for snapshots: one object URL per export, revoked when the component goes. */
function blobLinks(signal: AbortSignal) {
  const urls = new Map<string, string>();
  signal.addEventListener('abort', () => {
    for (const u of urls.values()) URL.revokeObjectURL(u);
  });
  return (ex: RoomExportView): string => {
    let u = urls.get(ex.id);
    if (!u) {
      u = URL.createObjectURL(new Blob([ex.content], { type: 'text/markdown' }));
      urls.set(ex.id, u);
    }
    return u;
  };
}

export function RoomExports(handle: Handle<{ room: Room }>) {
  const s = services(handle);
  const act = action(handle);
  let open = false;
  let shown: RoomExportView | undefined;
  /** Verify results by export id: true matches, a string says why not. */
  const verified = new Map<string, true | string>();
  const link = blobLinks(handle.signal);
  const list = query(handle, {
    load: () => s.client.roomExports(handle.props.room.id),
    key: () => handle.props.room.id,
    enabled: () => open,
  });
  const verifyAll = async (xs: readonly RoomExportView[]) => {
    for (const ex of xs) {
      if (verified.has(ex.id)) continue;
      try {
        const r = await s.client.roomExportVerify(ex.id);
        verified.set(ex.id, r.ok ? true : r.error || 'does not match');
      } catch (e) {
        verified.set(ex.id, e instanceof Error ? e.message : String(e));
      }
      void handle.update();
    }
  };
  const make = async () => {
    const r = await act.run('minutes', () => s.client.roomExport(handle.props.room.id, 'minutes'));
    if (!r) return;
    shown = r;
    await list.reload();
    void handle.update();
  };

  return () => {
    const room = handle.props.room;
    const minutes = (list.data ?? []).filter((x) => x.kind === 'minutes');
    if (open && minutes.some((x) => !verified.has(x.id))) handle.queueTask(() => void verifyAll(minutes));
    return (
      <>
        <Button
          size="sm"
          variant="ghost"
          icon={FileDown}
          aria-haspopup="dialog"
          mix={[
            on('click', () => {
              open = true;
              void handle.update();
            }),
          ]}
        >
          Minutes
        </Button>
        <Dialog
          open={open}
          wide
          title="Minutes"
          onClose={() => {
            open = false;
            void handle.update();
          }}
        >
          <p mix={introCss}>
            What was said and decided in #{room.name}, as Markdown. Each snapshot is kept with its hash and checked against the messages. Nothing leaves this
            machine.
          </p>
          <ErrorNote error={list.error} what="Could not list the minutes" />
          {minutes.length ? (
            <ul mix={listCss} aria-label="Kept minutes" data-testid="room-minutes">
              {minutes.map((ex) => {
                const v = verified.get(ex.id);
                return (
                  <li key={ex.id} data-testid="room-minutes-row">
                    <span class="when">{formatDateTime(ex.createdAt)}</span>
                    <span class="who">{madeBy(ex)}</span>
                    {v === true ? (
                      <span class="ok" data-testid="room-minutes-verified">
                        <Icon icon={CircleCheck} size={14} />
                        matches the messages
                      </span>
                    ) : v ? (
                      <span class="bad" data-testid="room-minutes-mismatch" title={v}>
                        <Icon icon={CircleX} size={14} />
                        does not match
                      </span>
                    ) : (
                      <span class="who">checking...</span>
                    )}
                    <Button variant="link" size="sm" mix={[on('click', () => ((shown = ex), void handle.update()))]}>
                      Read
                    </Button>
                    <a href={link(ex)} download={exportFileName(room.name, ex.kind, ex.format)} mix={buttonStyle('ghost', 'sm')} data-rmx-document>
                      <Icon icon={Download} size={14} />
                      Download
                    </a>
                  </li>
                );
              })}
            </ul>
          ) : list.data ? (
            <p mix={introCss} data-testid="room-minutes-none">
              No minutes kept yet.{room.kind === 'task' ? ' They are kept when the task closes.' : ''}
            </p>
          ) : null}
          <div mix={choices} class="room-exports">
            <Button icon={ScrollText} disabled={act.busy !== ''} mix={[on('click', () => void make())]}>
              {act.busy === 'minutes' ? 'Making minutes...' : 'Make minutes now'}
            </Button>
          </div>
          <ErrorNote error={act.error} what="Could not make the minutes" />
          {shown ? (
            <div mix={resultCss} data-testid="room-export">
              <pre class="export-text">{shown.content}</pre>
            </div>
          ) : null}
        </Dialog>
      </>
    );
  };
}

/** The task page's link to the minutes its room kept when the task closed (X19); nothing while there are none. */
export function TaskMinutesLink(handle: Handle<{ roomId: string; roomName?: string }>) {
  const s = services(handle);
  const link = blobLinks(handle.signal);
  const list = query(handle, {
    load: () => s.client.roomExports(handle.props.roomId),
    when: (e) => e.type === 'room.archived',
    key: () => handle.props.roomId,
  });
  return () => {
    const kept = (list.data ?? []).find((x) => x.kind === 'minutes' && x.scope === TASK_ARCHIVE_SCOPE);
    if (!kept) return null;
    return (
      <a href={link(kept)} download={exportFileName(handle.props.roomName || 'task', kept.kind, kept.format)} mix={buttonStyle('ghost', 'sm')} data-rmx-document data-testid="task-minutes">
        <Icon icon={ScrollText} size={14} />
        Minutes
      </a>
    );
  };
}
