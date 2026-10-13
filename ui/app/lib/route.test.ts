import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { routes } from '../routes.ts';
import { hashToPath, isWorkspacePage, isWorkspaceRoute, parseRoute } from './route.ts';

describe('routes', () => {
  it('builds the paths the screens link to', () => {
    expect(routes.task.href({ id: 'a b' })).toBe('/task/a%20b');
    expect(routes.run.href({ id: 't1' })).toBe('/task/t1/run');
    expect(routes.run.href({ id: 't1', runId: 'r1' })).toBe('/task/t1/run/r1');
    expect(routes.room.href({ id: 'r' })).toBe('/room/r');
    expect(routes.settings.href()).toBe('/settings');
  });

  it('parses every path back into the screen it shows', () => {
    expect(parseRoute('/')).toEqual({ name: 'home' });
    expect(parseRoute('/room/r%201')).toEqual({ name: 'room', id: 'r 1' });
    expect(parseRoute('/tasks/new')).toEqual({ name: 'task-new' });
    expect(parseRoute('/task/t1')).toEqual({ name: 'task', id: 't1' });
    expect(parseRoute('/task/t1/run')).toEqual({ name: 'run', taskId: 't1' });
    expect(parseRoute('/task/t1/run/r2')).toEqual({ name: 'run', taskId: 't1', runId: 'r2' });
    expect(parseRoute('/project/new')).toEqual({ name: 'project-new' });
    expect(parseRoute('/settings')).toEqual({ name: 'settings' });
    expect(parseRoute('/nope')).toEqual({ name: 'not-found' });
    expect(parseRoute('/team/extra')).toEqual({ name: 'not-found' });
  });

  it('opens the workspace screens, and /mcp as an alias of Integrations, without a project', () => {
    expect(isWorkspaceRoute({ name: 'integrations' })).toBe(true);
    expect(isWorkspaceRoute({ name: 'mcp' })).toBe(true);
    expect(isWorkspaceRoute({ name: 'tasks' })).toBe(false);
    expect(parseRoute('/mcp')).toEqual({ name: 'mcp' });
  });

  it('has a workspace Home and a skill library page, both outside any project', () => {
    expect(routes.workspace.href()).toBe('/home');
    expect(routes.librarySkills.href()).toBe('/library/skills');
    expect(parseRoute('/home')).toEqual({ name: 'workspace' });
    expect(parseRoute('/library/skills')).toEqual({ name: 'library-skills' });
    expect(parseRoute('/library')).toEqual({ name: 'library' });
    expect(parseRoute('/library/other')).toEqual({ name: 'not-found' });
    expect(parseRoute('/workspace')).toEqual({ name: 'not-found' });
    expect(parseRoute('/library-skills')).toEqual({ name: 'not-found' });
    expect(isWorkspaceRoute({ name: 'workspace' })).toBe(true);
    expect(isWorkspaceRoute({ name: 'library-skills' })).toBe(true);
  });

  it('tells workspace pages (the workspace sidebar) from project pages', () => {
    for (const p of ['/home', '/chats', '/chats/c1', '/library', '/library/skills', '/integrations', '/mcp', '/settings', '/project/new']) {
      expect([p, isWorkspacePage(parseRoute(p))]).toEqual([p, true]);
    }
    for (const p of ['/', '/needs-you', '/room/r', '/tasks', '/task/t', '/skills', '/project', '/team']) {
      expect([p, isWorkspacePage(parseRoute(p))]).toEqual([p, false]);
    }
  });

  it('turns old hash links into paths', () => {
    expect(hashToPath('#/task/x')).toBe('/task/x');
    expect(hashToPath('#/')).toBe('/');
    expect(hashToPath('#main')).toBeUndefined();
    expect(hashToPath('')).toBeUndefined();
  });
});
