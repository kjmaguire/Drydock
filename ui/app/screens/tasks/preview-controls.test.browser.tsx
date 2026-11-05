import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { PreviewInfo } from '../../api/extTypes.ts';
import { byText, cleanup, mount, waitFor } from '../../test/render.tsx';
import { PreviewControls } from './preview-controls.tsx';
import { svc } from './testing.ts';

afterEach(cleanup);

const running: PreviewInfo = { taskId: 't1', url: 'http://127.0.0.1:5173/', pid: 42 };

describe("A task's preview", () => {
  it('links its setting under Project settings and offers Start preview', async () => {
    const r = mount(<PreviewControls taskId="t1" hasWorktree={true} />, svc());
    await waitFor(() => expect(r.$('section[aria-label="Preview"]')).not.toBe(null), r);
    expect(r.$('a[data-testid="preview-settings"]')!.getAttribute('href')).toBe('/project');
    expect(byText(r.container, 'button', 'Start preview')).not.toBe(null);
    expect(r.$('[data-testid="preview-open"]')).toBe(null);
  });

  it('is not offered in a sandboxed project, where the core refuses to run it here (X33)', async () => {
    let read = false;
    const r = mount(
      <PreviewControls taskId="t1" hasWorktree={true} sandboxed={true} />,
      svc({
        previewList: async () => {
          read = true;
          return [];
        },
      }),
    );
    await waitFor(() => expect(read).toBe(true), r);
    expect(r.$('section[aria-label="Preview"]')).toBe(null);
    expect(r.container.textContent ?? '').not.toContain('Start preview');
  });

  it('a running preview opens with "Open the app", and one left from before the sandbox can still be stopped (X32)', async () => {
    const r = mount(<PreviewControls taskId="t1" hasWorktree={true} sandboxed={true} />, svc({ previewList: async () => [running] }));
    const open = await waitFor(() => {
      const a = r.$('a[data-testid="preview-open"]');
      if (!a) throw new Error('not yet');
      return a;
    }, r);
    expect(open.textContent).toBe('Open the app');
    expect(open.getAttribute('href')).toBe('http://127.0.0.1:5173/');
    expect(open.getAttribute('target')).toBe('_blank');
    expect(open.hasAttribute('data-rmx-document')).toBe(true);
    expect(byText(r.container, 'button', 'Stop preview')).not.toBe(null);
  });
});
