import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { routes } from '../routes.ts';
import { hashToPath, hubOf, isWorkspacePage, isWorkspaceRoute, parseRoute } from './route.ts';

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

  it('tells workspace pages (the workspace sidebar) from project pages', () => {
    for (const p of ['/home', '/chats', '/chats/c1', '/library', '/library/skills', '/integrations', '/mcp', '/settings', '/project/new']) {
      expect([p, isWorkspacePage(parseRoute(p))]).toEqual([p, true]);
    }
    for (const p of ['/', '/needs-you', '/room/r', '/tasks', '/task/t', '/skills', '/project', '/team', '/project/integrations']) {
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
    expect(hubOf({ name: 'needs-you' })).toBe('none'); // the bell's page lights no tab
    expect(isWorkspacePage({ name: 'agent', id: 'agt_1' })).toBe(false);
  });

  it('turns old hash links into paths', () => {
    expect(hashToPath('#/task/x')).toBe('/task/x');
    expect(hashToPath('#/')).toBe('/');
    expect(hashToPath('#main')).toBeUndefined();
    expect(hashToPath('')).toBeUndefined();
  });
});
