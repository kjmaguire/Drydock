import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { RoomExportView } from '../../api/runClient.ts';
import type { Room } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { RoomExports, TaskMinutesLink } from './room-exports.tsx';

afterEach(cleanup);

const room: Room = { id: 'room_1', projectId: 'p', kind: 'task', name: 'task-3-login', mode: 'auto', archived: true, createdAt: '', version: 1, taskId: 't3' };
const kept = (over: Partial<RoomExportView>): RoomExportView => ({
  id: 'ex1',
  roomId: 'room_1',
  kind: 'minutes',
  format: 'markdown',
  content: '# Minutes\n\n- decided',
  sha256: 'abcdef0123456789',
  createdAt: '2026-11-01T10:00:00Z',
  scope: 'system:task-archive',
  receiptId: 't3',
  ...over,
});

describe('RoomExports (X19)', () => {
  it('lists the kept minutes with their verify ticks and Download, and makes new minutes on request', async () => {
    const made: unknown[][] = [];
    let list = [kept({}), kept({ id: 'ex0', scope: 'human:kyle', createdAt: '2026-10-30T10:00:00Z' }), kept({ id: 'adr', kind: 'adr_candidates', format: 'json' })];
    const s = testServices({
      roomExports: async () => list,
      roomExportVerify: async (id: string) => (id === 'ex0' ? { ok: false, error: 'the content hash does not match' } : { ok: true, error: '' }),
      roomExport: async (...a: unknown[]) => {
        made.push(a);
        const ex = kept({ id: 'ex2', scope: 'human:kyle', content: '# Minutes\n\n- new' });
        list = [ex, ...list];
        return ex;
      },
    });
    const r = mount(<RoomExports room={room} />, s);
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Minutes').click());
    const rows = await waitFor(() => {
      const rs = Array.from(document.querySelectorAll<HTMLElement>('[data-testid="room-minutes-row"]'));
      if (rs.length !== 2) throw new Error(`rows ${rs.length}`);
      return rs;
    }, r);
    expect(rows[0]!.textContent).toContain('kept when the task closed');
    expect(rows[1]!.textContent).toContain('made on request');
    await waitFor(() => expect(rows[0]!.querySelector('[data-testid="room-minutes-verified"]')).toBeTruthy(), r);
    await waitFor(() => expect(rows[1]!.querySelector('[data-testid="room-minutes-mismatch"]')!.getAttribute('title')).toBe('the content hash does not match'), r);
    expect(byText<HTMLAnchorElement>(rows[0]!, 'a', 'Download').getAttribute('download')).toBe('task-3-login-minutes.md');
    await r.act(() => byText<HTMLButtonElement>(rows[0]!, 'button', 'Read').click());
    expect(document.querySelector('[data-testid="room-export"] pre')!.textContent).toBe('# Minutes\n\n- decided');
    await r.act(() => byText<HTMLButtonElement>(document.body, 'button', 'Make minutes now').click());
    await waitFor(() => expect(made).toEqual([['room_1', 'minutes']]), r);
    await waitFor(() => expect(document.querySelectorAll('[data-testid="room-minutes-row"]')).toHaveLength(3), r);
    expect(document.querySelector('[data-testid="room-export"] pre')!.textContent).toBe('# Minutes\n\n- new');
    // decisions are listed in #decisions now: no ADR export here
    expect(document.body.textContent).not.toContain('ADR candidates');
  });

  it('says when no minutes are kept, and shows the core refusal', async () => {
    const s = testServices({
      roomExports: async () => [],
      roomExport: async () => {
        throw new Error('nothing to export');
      },
    });
    const r = mount(<RoomExports room={room} />, s);
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Minutes').click());
    await waitFor(() => expect(document.querySelector('[data-testid="room-minutes-none"]')!.textContent).toBe('No minutes kept yet. They are kept when the task closes.'), r);
    await r.act(() => byText<HTMLButtonElement>(document.body, 'button', 'Make minutes now').click());
    await waitFor(() => expect(document.querySelector('[role="alert"]')!.textContent).toContain('nothing to export'), r);
  });

  it("the task page's link downloads the minutes kept when the task closed, and is absent until then", async () => {
    const r = mount(<TaskMinutesLink roomId="room_1" roomName="task-3" />, testServices({ roomExports: async () => [kept({ id: 'ex0', scope: 'human:kyle' }), kept({})] }));
    const a = await waitFor(() => {
      const x = r.container.querySelector<HTMLAnchorElement>('[data-testid="task-minutes"]');
      if (!x) throw new Error('no link yet');
      return x;
    }, r);
    expect(a.getAttribute('download')).toBe('task-3-minutes.md');
    expect(a.getAttribute('href')!.startsWith('blob:')).toBe(true);
    cleanup();
    const none = mount(<TaskMinutesLink roomId="room_1" />, testServices({ roomExports: async () => [kept({ scope: 'human:kyle' })] }));
    await new Promise((res) => setTimeout(res, 50));
    expect(none.container.querySelector('[data-testid="task-minutes"]')).toBeNull();
  });
});
