import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { Project, Room, Task } from '../api/types.ts';
import { FakeTransport, ManualScheduler } from '../test/fakes.ts';
import { EventStore } from './eventStore.ts';
import { ProjectData } from './project.ts';
import { Workspace } from './workspace.ts';

const task = (seq: number): Task => ({ id: `t${seq}`, projectId: 'p1', seq, title: `Task ${seq}`, description: '', status: 'BACKLOG', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1 });

/** Waits (polling, bounded) for something that is going to happen. */
async function until(check: () => boolean, ms = 2000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > ms) throw new Error('timed out');
    await new Promise((r) => setTimeout(r, 5));
  }
}

// TEST DOUBLES (labelled): a partial client around the project's shared reads; the ones this test is not about answer empty.
function open(client: Partial<Client>) {
  const store = new EventStore(new FakeTransport(), { scheduler: new ManualScheduler(), backoff: () => 100 });
  const c = {
    agentList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    presenceList: async () => [],
    ...client,
  } as Client;
  const ws = new Workspace(c, store, { persist: false });
  const data = new ProjectData(c, store, ws).start();
  ws.projects.set([{ id: 'p1', name: 'gocalc' } as Project]);
  return { store, data };
}

describe('the project\'s task read says how much of the project it has (M21)', () => {
  it('reads the newest tasks and how many there are in one go, and is partial when there are more', async () => {
    const reads: string[] = [];
    const { data } = open({
      taskList: async (id) => (reads.push(`list ${id}`), [task(3), task(2)]),
      taskCount: async (id) => (reads.push(`count ${id}`), { total: 7 }),
    });
    await until(() => data.taskList.length === 2);
    expect(data.taskTotal).toBe(7);
    expect(data.taskCoverage).toEqual({ loaded: 2, total: 7, partial: true });
    expect(reads.sort()).toEqual(['count p1', 'list p1']);
  });

  it('is complete when the count equals what was listed', async () => {
    const { data } = open({ taskList: async () => [task(2), task(1)], taskCount: async () => ({ total: 2 }) });
    await until(() => data.taskList.length === 2);
    expect(data.taskCoverage).toEqual({ loaded: 2, total: 2, partial: false });
  });

  it('shows the list all the same when the count fails or the core has none, and then never calls a list as long as the core sends it complete', async () => {
    const full = Array.from({ length: 500 }, (_, i) => task(500 - i));
    const failing = open({
      taskList: async () => full,
      taskCount: async () => {
        throw new Error('task.count: the database is busy');
      },
    });
    await until(() => failing.data.taskList.length === 500);
    expect(failing.data.taskTotal).toBeUndefined();
    expect(failing.data.taskCoverage).toEqual({ loaded: 500, total: undefined, partial: true });
    expect(failing.data.tasks.error).toBeUndefined();

    const missing = open({ taskList: async () => [task(1)] }); // no taskCount at all
    await until(() => missing.data.taskList.length === 1);
    expect(missing.data.taskCoverage).toEqual({ loaded: 1, total: undefined, partial: false });
  });

  it('reads both again on a task event, so the numbers move together', async () => {
    let total = 1;
    let listed: Task[] = [task(1)];
    const { data, store } = open({ taskList: async () => listed, taskCount: async () => ({ total }) });
    await until(() => data.taskList.length === 1);
    listed = [task(2), task(1)];
    total = 9;
    store.ingest({ seq: 5, time: '2026-01-01T00:00:00Z', type: 'task.created', projectId: 'p1' });
    await until(() => data.taskList.length === 2);
    expect(data.taskCoverage).toEqual({ loaded: 2, total: 9, partial: true });
  });

  it('shows nothing about tasks before the first answer', () => {
    const { data } = open({ taskList: () => new Promise<Task[]>(() => undefined), taskCount: async () => ({ total: 3 }) });
    expect(data.taskList).toEqual([]);
    expect(data.taskTotal).toBeUndefined();
    expect(data.taskCoverage.partial).toBe(false);
  });
});

describe('a direct message opened from a page is in the room list before it is shown (decision P130)', () => {
  it('reads the rooms again when the DM is in the open project, so Ctrl+. and the Rooms rail know it at once', async () => {
    const dm = { id: 'r9', projectId: 'p1', kind: 'dm', agentId: 'a1', name: 'dm' } as Room;
    let rooms: Room[] = [];
    const made: string[] = [];
    const { data } = open({ roomList: async () => rooms, roomCreateDM: async (pid, aid) => (made.push(`${pid} ${aid}`), (rooms = [dm]), dm) });
    await until(() => data.id === 'p1');
    const r = await data.openDM('p1', 'a1');
    expect(r.id).toBe('r9');
    expect(made).toEqual(['p1 a1']);
    // no room.created event came: the read after the create is what put it there
    expect(data.roomList.find((x) => x.id === 'r9')?.agentId).toBe('a1');
  });

  it('leaves the open project\'s rooms alone for a DM in another project', async () => {
    let reads = 0;
    const { data } = open({ roomList: async () => (reads++, []), roomCreateDM: async () => ({ id: 'r8', projectId: 'p2', kind: 'dm' }) as Room });
    await until(() => data.id === 'p1' && reads > 0);
    const before = reads;
    await data.openDM('p2', 'a7');
    expect(reads).toBe(before);
  });
});
