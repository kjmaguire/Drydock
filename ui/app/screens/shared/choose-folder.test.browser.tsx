import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ChooseFolder } from './choose-folder.tsx';

afterEach(cleanup);

function show(client: Partial<Client>, picked: string[]) {
  const s = testServices(client);
  return mount(<ChooseFolder title="Choose the folder to scan" testId="pick" onPick={(p) => picked.push(p)} />, s);
}

describe('ChooseFolder', () => {
  it('opens the window folder dialog through the core and hands over the folder; cancelling hands over nothing', async () => {
    const titles: string[] = [];
    const answers = ['D:\\code\\shop', ''];
    const picked: string[] = [];
    const r = show(
      {
        fsFolderHints: async () => ({ canPick: true, windows: true, devDrives: [] }),
        fsPickFolder: async (title?: string) => (titles.push(title ?? ''), answers.shift() ?? ''),
      },
      picked,
    );
    const button = await waitFor(() => {
      const b = r.$('[data-testid="pick"]') as HTMLButtonElement | null;
      if (!b) throw new Error('not yet');
      return b;
    }, r);
    expect(button.textContent).toContain('Choose folder');
    await r.act(() => button.click());
    await waitFor(() => expect(picked).toEqual(['D:\\code\\shop']), r);
    expect(titles).toEqual(['Choose the folder to scan']);
    await waitFor(() => expect((r.$('[data-testid="pick"]') as HTMLButtonElement).disabled).toBe(false), r);
    await r.act(() => (r.$('[data-testid="pick"]') as HTMLButtonElement).click());
    await waitFor(() => expect(titles.length).toBe(2), r);
    expect(picked).toEqual(['D:\\code\\shop']);
  });

  it('compact: an icon button named "Choose folder…" that does the same', async () => {
    const picked: string[] = [];
    const s = testServices({ fsFolderHints: async () => ({ canPick: true, windows: true, devDrives: [] }), fsPickFolder: async () => 'D:\\code\\shop' });
    const r = mount(<ChooseFolder title="Choose the folder to scan" testId="pick" compact onPick={(p) => picked.push(p)} />, s);
    const button = await waitFor(() => {
      const b = r.$('[data-testid="pick"]') as HTMLButtonElement | null;
      if (!b) throw new Error('not yet');
      return b;
    }, r);
    expect(button.getAttribute('aria-label')).toBe('Choose folder…');
    expect(button.textContent).toBe('');
    await r.act(() => button.click());
    await waitFor(() => expect(picked).toEqual(['D:\\code\\shop']), r);
  });

  it('is not offered where the window has no folder dialog (the browser in development)', async () => {
    let asked = false;
    const r = show(
      {
        fsFolderHints: async () => {
          asked = true;
          return { canPick: false, windows: false, devDrives: [] };
        },
      },
      [],
    );
    await waitFor(() => expect(asked).toBe(true), r);
    expect(r.$('[data-testid="pick"]')).toBeNull();
  });
});
