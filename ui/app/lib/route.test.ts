import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { routes } from '../routes.ts';
import { hashToPath, hubOf, isWorkspaceHub, isWorkspacePage, isWorkspaceRoute, parseRoute, redirectLegacyHash } from './route.ts';

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
    expect(parseRoute('/team/agt%201')).toEqual({ name: 'agent', id: 'agt 1' });
    expect(parseRoute('/team/agt/x')).toEqual({ name: 'not-found' });
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

  it("has the workspace's Crew page: every project's agents, outside any project", () => {
    expect(routes.crew.href()).toBe('/crew');
    expect(parseRoute('/crew')).toEqual({ name: 'crew' });
    expect(parseRoute('/crew/x')).toEqual({ name: 'not-found' });
    expect(isWorkspaceRoute({ name: 'crew' })).toBe(true);
    expect(hubOf({ name: 'crew' })).toBe('workspace-crew');
    expect(isWorkspaceHub(hubOf({ name: 'crew' }))).toBe(true);
    // the project's own Crew stays a project page
    expect(hubOf({ name: 'team' })).toBe('crew');
  });

  it('tells workspace pages (the workspace sidebar) from project pages', () => {
    for (const p of ['/home', '/needs-you', '/my-tasks', '/crew', '/chats', '/chats/c1', '/library', '/library/skills', '/integrations', '/mcp', '/settings', '/project/new']) {
      expect([p, isWorkspacePage(parseRoute(p))]).toEqual([p, true]);
    }
    for (const p of ['/', '/room/r', '/tasks', '/task/t', '/skills', '/project', '/team', '/project/integrations']) {
      expect([p, isWorkspacePage(parseRoute(p))]).toEqual([p, false]);
    }
  });

  it("has a project's own Integrations page, a project page that needs a project", () => {
    expect(routes.projectIntegrations.href()).toBe('/project/integrations');
    expect(parseRoute('/project/integrations')).toEqual({ name: 'project-integrations' });
    expect(parseRoute('/project/integrations/x')).toEqual({ name: 'not-found' });
    expect(isWorkspaceRoute({ name: 'project-integrations' })).toBe(false);
    expect(isWorkspacePage({ name: 'project-integrations' })).toBe(false);
    // the workspace's Integrations page stays a workspace page
    expect(isWorkspacePage(parseRoute('/integrations'))).toBe(true);
  });

  it("puts a project's settings and Integrations in its Overview hub, and the GitHub page behind the gear", () => {
    expect(hubOf({ name: 'project' })).toBe('overview');
    expect(hubOf({ name: 'project-integrations' })).toBe('overview');
    expect(routes.github.href()).toBe('/github');
    expect(parseRoute('/github')).toEqual({ name: 'github' });
    expect(parseRoute('/github/x')).toEqual({ name: 'not-found' });
    expect(hubOf({ name: 'github' })).toBe('settings');
    // the page is the workspace's: it opens without a project, like Connections
    expect(isWorkspaceRoute({ name: 'github' })).toBe(true);
    expect(isWorkspacePage(parseRoute('/github'))).toBe(true);
  });

  it('has a workspace Security scan of any folder, beside the project\'s own Security page (decision P61)', () => {
    expect(routes.securityScan.href()).toBe('/security-scan');
    expect(parseRoute('/security-scan')).toEqual({ name: 'security-scan' });
    expect(parseRoute('/security-scan/x')).toEqual({ name: 'not-found' });
    expect(isWorkspaceRoute({ name: 'security-scan' })).toBe(true);
    expect(isWorkspacePage(parseRoute('/security-scan'))).toBe(true);
    // the project's own Security page needs its project
    expect(isWorkspacePage(parseRoute('/security'))).toBe(false);
  });

  it("has the project's Repo map page (decision P62)", () => {
    expect(routes.repoMap.href()).toBe('/repo-map');
    expect(parseRoute('/repo-map')).toEqual({ name: 'repo-map' });
    expect(parseRoute('/repo-map/x')).toEqual({ name: 'not-found' });
    expect(isWorkspacePage(parseRoute('/repo-map'))).toBe(false);
  });

  it("has an agent's page under the Crew tab (decision P64)", () => {
    expect(routes.agent.href({ id: 'agt_1' })).toBe('/team/agt_1');
    expect(parseRoute('/team')).toEqual({ name: 'team' });
    expect(parseRoute('/team/agt_1')).toEqual({ name: 'agent', id: 'agt_1' });
    expect(hubOf({ name: 'agent', id: 'agt_1' })).toBe('crew');
    // the Inbox and My tasks are the workspace's (decision P97): they open without a project and light no project's tab
    expect(hubOf({ name: 'needs-you' })).toBe('inbox');
    expect(hubOf({ name: 'my-tasks' })).toBe('inbox');
    expect(isWorkspaceRoute({ name: 'needs-you' })).toBe(true);
    expect(parseRoute('/my-tasks')).toEqual({ name: 'my-tasks' });
    expect(routes.myTasks.href()).toBe('/my-tasks');
    expect(parseRoute('/needs-you/x')).toEqual({ name: 'not-found' });
    expect(isWorkspacePage({ name: 'agent', id: 'agt_1' })).toBe(false);
  });

  it('turns old hash links into paths', () => {
    expect(hashToPath('#/task/x')).toBe('/task/x');
    expect(hashToPath('#/')).toBe('/');
    expect(hashToPath('#main')).toBeUndefined();
    expect(hashToPath('')).toBeUndefined();
  });

  it('turns no hash into a path that names another place or that a browser would refuse (L42)', () => {
    for (const hash of ['#//evil.example/x', '#///evil.example', '#/\\evil.example', '#/task/a\\b', '#/task/\u0000', '#/task/a\nb', '#/task/\u007f']) {
      expect([hash, hashToPath(hash)]).toEqual([hash, undefined]);
    }
    // a bad percent escape is left as written: nothing decodes it here, and the router shows what it can
    expect(hashToPath('#/task/%E0%A4%A')).toBe('/task/%E0%A4%A');
    expect(hashToPath('#/task/%')).toBe('/task/%');
    expect(parseRoute(hashToPath('#/task/%E0%A4%A')!)).toEqual({ name: 'task', id: '%E0%A4%A' });
    expect(hashToPath('#/tasks?x=1')).toBe('/tasks?x=1');
  });

  it('moves to the screen an old link names and keeps the query, but never throws (L42)', () => {
    // TEST DOUBLE: history.replaceState as a browser does it, refusing an address on another origin
    const moves: string[] = [];
    const history = {
      replaceState: (_s: unknown, _t: string, url: string) => {
        if (new URL(url, 'http://localhost').origin !== 'http://localhost') throw new DOMException('cross-origin', 'SecurityError');
        moves.push(url);
      },
    };
    expect(redirectLegacyHash({ hash: '#/task/x', search: '?token=abc' }, history)).toBe(true);
    expect(moves).toEqual(['/task/x?token=abc']);
    for (const hash of ['', '#main', '#//evil.example/x', '#/\\evil.example', '#/task/%E0%A4%A']) {
      expect(() => redirectLegacyHash({ hash, search: '' }, history)).not.toThrow();
    }
    expect(moves).toEqual(['/task/x?token=abc', '/task/%E0%A4%A']);
    // a browser that refuses an address it was given leaves the page where it is
    const refusing = { replaceState: () => { throw new DOMException('no', 'SecurityError'); } };
    const warn = console.warn;
    console.warn = () => {};
    try {
      expect(redirectLegacyHash({ hash: '#/task/x', search: '' }, refusing)).toBe(false);
    } finally {
      console.warn = warn;
    }
  });
});
