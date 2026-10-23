// TEST DOUBLES (labelled) for the task and pull request screen tests: plain data fixtures and services around a partial
// client with the reads the shell's project model makes. Nothing here imitates an agent.

import type { Client } from '../../api/client.ts';
import type { LicenseView } from '../../api/pkgTypes.ts';
import type { Agent, Task, TaskDetail } from '../../api/types.ts';
import { lic } from '../../test/fixtures.ts';
import { testServices, type TestServices } from '../../test/render.tsx';

export const agent = (id: string, name: string, role: string): Agent =>
  ({ id, projectId: 'prj_1', name, adapter: 'claude', role, model: '', effort: '', providerId: '', permissionMode: '', status: '' }) as Agent;

export const task = (over: Partial<Task> = {}): Task => ({
  id: 't1',
  projectId: 'prj_1',
  seq: 1,
  title: 'Add Multiply',
  description: '',
  status: 'BACKLOG',
  priority: 0,
  assignee: 'a1',
  reviewer: 'a2',
  iteration: 0,
  maxIterations: 5,
  costUsd: 0,
  created: 0,
  updated: 0,
  version: 1,
  ...over,
});

export const detail = (over: Partial<TaskDetail> = {}): TaskDetail => ({ task: task(), evidence: [], reviews: [], pending: [], ...over });

export const AGENTS = [agent('a1', 'Dana', 'backend'), agent('a2', 'Rex', 'reviewer')];

/** Services with a selected project (prj_1), the given license, and the project reads answered. */
export function svc(client: Partial<Client> = {}, opts: { license?: LicenseView; tasks?: Task[]; agents?: Agent[] } = {}): TestServices {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    licenseStatus: async () => opts.license ?? lic(),
    appInfo: async () => ({}) as never,
    agentList: async () => opts.agents ?? AGENTS,
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    presenceList: async () => [],
    taskList: async () => opts.tasks ?? [],
    templateList: async () => [],
    usageList: async () => [],
    routeDecisions: async () => [],
    previewList: async () => [],
    taskActivity: async () => ({ threads: [] }),
    taskSummaries: async () => ({ summaries: [], total: 0, partial: false }),
    connectionList: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

/** Records the arguments of each call (a labelled test double for a client method). */
export function recorder<A extends unknown[], R>(result: (...args: A) => R | Promise<R>) {
  const calls: A[] = [];
  const fn = async (...args: A): Promise<R> => {
    calls.push(args);
    return result(...args);
  };
  return Object.assign(fn, { calls });
}
