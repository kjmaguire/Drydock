import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ChooseFile, ChooseSave, SavedFile } from './file-actions.tsx';

afterEach(cleanup);

const hints = (h: Record<string, boolean>) => async () => ({ canPick: false, windows: true, devDrives: [], ...h });

describe('file dialogs and a saved file (A11)', () => {
  it('opens the Save dialog at the path in the field, only where the window has one', async () => {
    const asked: unknown[] = [];
    const picked: string[] = [];
    const s = testServices({ fsFolderHints: hints({ canPickSave: true }), fsPickSave: async (kind, dir) => (asked.push([kind, dir]), 'D:\\out\\b.zip') });
    const r = mount(<ChooseSave kind="support-bundle" dir=" D:\out\a.zip " onPick={(p) => picked.push(p)} />, s);
    const btn = await waitFor(() => {
      const b = r.$('[data-testid="choose-save"]');
      if (!b) throw new Error('no Choose yet');
      return b as HTMLButtonElement;
    }, r);
    expect(btn.textContent).toBe('Choose…');
    await r.act(() => btn.click());
    await waitFor(() => expect(picked).toEqual(['D:\\out\\b.zip']), r);
    expect(asked).toEqual([['support-bundle', 'D:\\out\\a.zip']]);

    cleanup();
    const none = mount(<ChooseSave kind="support-bundle" dir="" onPick={() => {}} />, testServices({ fsFolderHints: hints({}) }));
    await new Promise((res) => setTimeout(res, 50));
    none.root.flush();
    expect(none.$('[data-testid="choose-save"]')).toBeNull();
  });

  it('chooses a license file with the Open dialog', async () => {
    const kinds: string[] = [];
    const picked: string[] = [];
    const s = testServices({ fsFolderHints: hints({ canPickFile: true }), fsPickFile: async (kind) => (kinds.push(kind), 'C:\\x\\a.drydock-license') });
    const r = mount(<ChooseFile kind="license" onPick={(p) => picked.push(p)} />, s);
    const btn = await waitFor(() => {
      const b = r.$('[data-testid="choose-file"]');
      if (!b) throw new Error('no Choose file yet');
      return b as HTMLButtonElement;
    }, r);
    await r.act(() => btn.click());
    await waitFor(() => expect(picked).toEqual(['C:\\x\\a.drydock-license']), r);
    expect(kinds).toEqual(['license']);
  });

  it('shows a saved file in its folder where the window can, and always offers Copy path', async () => {
    const shown: string[] = [];
    const s = testServices({ fsFolderHints: hints({ canReveal: true }), fsReveal: async (p) => void shown.push(p) });
    const r = mount(<SavedFile path="D:\out\b.zip" />, s);
    const show = await waitFor(() => {
      const b = r.$('[data-testid="saved-show"]');
      if (!b) throw new Error('no Show in folder yet');
      return b as HTMLButtonElement;
    }, r);
    expect(show.textContent).toBe('Show in folder');
    await r.act(() => show.click());
    await waitFor(() => expect(shown).toEqual(['D:\\out\\b.zip']), r);
    expect(r.$('[data-testid="saved-copy"]')!.textContent).toBe('Copy path');

    cleanup();
    const plain = mount(<SavedFile path="D:\out\b.zip" />, testServices({ fsFolderHints: hints({}) }));
    await waitFor(() => expect(plain.$('[data-testid="saved-copy"]')).toBeTruthy(), plain);
    await new Promise((res) => setTimeout(res, 50));
    plain.root.flush();
    expect(plain.$('[data-testid="saved-show"]')).toBeNull();
  });
});
