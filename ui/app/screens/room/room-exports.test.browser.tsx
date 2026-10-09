import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Room } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { RoomExports } from './room-exports.tsx';

afterEach(cleanup);

const room: Room = { id: 'room_1', projectId: 'p', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1 };

describe('RoomExports', () => {
  it('exports minutes as a kept snapshot, shows it and offers a download', async () => {
    const asked: unknown[][] = [];
    const s = testServices({
      roomExport: async (...a: unknown[]) => (asked.push(a), { id: 'ex1', roomId: 'room_1', kind: 'minutes', format: 'markdown', content: '# Minutes\n\n- decided', sha256: 'abcdef0123456789', createdAt: '' }),
    });
    const r = mount(<RoomExports room={room} />, s);
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Export').click());
    await r.act(() => byText<HTMLButtonElement>(document.body, 'button', 'Minutes (Markdown)').click());
    const out = await waitFor(() => document.querySelector<HTMLElement>('[data-testid="room-export"]')!, r);
    expect(asked).toEqual([['room_1', 'minutes']]);
    expect(out.textContent).toContain('abcdef012345');
    expect(out.querySelector('pre')!.textContent).toBe('# Minutes\n\n- decided');
    const link = byText<HTMLAnchorElement>(out, 'a', 'Download');
    expect(link.getAttribute('download')).toBe('general-minutes.md');
  });

  it('shows the core refusal', async () => {
    const s = testServices({
      roomExport: async () => {
        throw new Error('nothing to export');
      },
    });
    const r = mount(<RoomExports room={room} />, s);
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Export').click());
    await r.act(() => byText<HTMLButtonElement>(document.body, 'button', 'ADR candidates').click());
    await waitFor(() => expect(document.querySelector('[role="alert"]')!.textContent).toContain('nothing to export'), r);
  });
});
