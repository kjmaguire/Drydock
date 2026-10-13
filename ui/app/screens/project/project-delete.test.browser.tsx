import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Project } from '../../api/types.ts';
import { cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { DeleteProject } from './project-settings-screen.tsx';

afterEach(cleanup);

const project = { id: 'p1', name: 'gocalc', repoPath: 'C:\\code\\gocalc', hasRepo: true } as Project;

// TEST DOUBLES only: projectDelete records its calls and answers like the core.
function show(projectDelete: Client['projectDelete']) {
  const s = testServices({ licenseStatus: async () => ({}) as never, appInfo: async () => ({}) as never, projectList: async () => [project], projectDelete });
  s.workspace.start();
  const navigated: string[] = [];
  const r = mount(<DeleteProject project={project} onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated };
}

describe('deleting a project', () => {
  it('says what goes and what stays: the repository, its branches and the chats stay', () => {
    const { r } = show(async () => ({ name: 'gocalc', worktreesRemoved: 0, warnings: [] }));
    const text = r.$('[data-testid="delete-project"]')!.textContent!;
    expect(text).toContain('cannot be undone');
    expect(text).toContain('its tasks, their runs and the worktrees Drydock made for them');
    expect(text).toContain('The repository C:\\code\\gocalc, its branches and its commits are not touched.');
    expect(text).toContain('Your chats stay');
    expect(r.$('[data-testid="delete-project"] a[href="/data"]')!.textContent).toBe('export the project');
  });

  it('asks for the name; only the exact name enables Delete; then it deletes, leaves a note and opens Home', async () => {
    const calls: [string, string][] = [];
    const { r, s, navigated } = show(async (id, name) => (calls.push([id, name]), { name: 'gocalc', worktreesRemoved: 2, warnings: [] }));
    expect(r.$('[data-testid="delete-confirm"]')).toBeNull();
    await r.act(() => (r.$('[data-testid="delete-project-open"]') as HTMLButtonElement).click());
    const input = r.$('[data-testid="delete-confirm"]') as HTMLInputElement;
    const confirm = () => r.$('[data-testid="delete-project-confirm"]') as HTMLButtonElement;
    expect(confirm().disabled).toBe(true);
    await r.act(() => type(input, 'gocal'));
    expect(confirm().disabled).toBe(true);
    await r.act(() => type(input, 'gocalc'));
    expect(confirm().disabled).toBe(false);
    await r.act(() => confirm().click());
    await waitFor(() => expect(navigated).toEqual(['/home']), r);
    expect(calls).toEqual([['p1', 'gocalc']]);
    expect(s.ui.takeFlash()).toEqual({ text: 'gocalc was deleted.', tone: 'ok' });
  });

  it("shows the core's refusal (work is running) and stays on the page", async () => {
    const { r, navigated } = show(async () => {
      throw new Error('work is running in this project: task #3 Subtract is running. Cancel it (or wait for it to end), then delete the project');
    });
    await r.act(() => (r.$('[data-testid="delete-project-open"]') as HTMLButtonElement).click());
    await r.act(() => type(r.$('[data-testid="delete-confirm"]') as HTMLInputElement, 'gocalc'));
    await r.act(() => (r.$('[data-testid="delete-project-confirm"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="delete-project"]')!.textContent).toContain('task #3 Subtract is running'), r);
    expect(navigated).toEqual([]);
  });

  it('says what could not be cleaned up', async () => {
    const { r, s } = show(async () => ({ name: 'gocalc', worktreesRemoved: 0, warnings: ['the folder C:\\x: in use'] }));
    await r.act(() => (r.$('[data-testid="delete-project-open"]') as HTMLButtonElement).click());
    await r.act(() => type(r.$('[data-testid="delete-confirm"]') as HTMLInputElement, 'gocalc'));
    await r.act(() => (r.$('[data-testid="delete-project-confirm"]') as HTMLButtonElement).click());
    await waitFor(() => expect(s.ui.takeFlash()).toEqual({ text: 'gocalc was deleted. Not cleaned up: the folder C:\\x: in use', tone: 'warn' }), r);
  });
});
