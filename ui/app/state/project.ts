// The selected project's shared reads: agents, rooms, unread counts, Needs You, presence and tasks. The shell, the details
// panel, the palette and most screens need them, so they are read once here and re-read on the events that change them.

import type { Client } from '../api/client.ts';
import type { Agent, DrydockEvent, NeedsYouItem, PresenceState, Room, Task, Unread } from '../api/types.ts';
import { taskCoverage, type TaskCoverage } from '../lib/task-total.ts';
import type { EventStore } from './eventStore.ts';
import { Live, starts } from './live.ts';
import { Model } from './services.tsx';
import type { Workspace } from './workspace.ts';

/** The events after which Needs you may have changed: every list or count of it reloads on these, so all of them agree. */
export const needsChanged = (e: DrydockEvent): boolean =>
  starts(e, 'approval.', 'workflow.request', 'task.', 'watchdog.', 'room.message_posted', 'room.archived', 'chat.permission', 'needsyou.');

/** The project's tasks as one read: the newest ones the core lists, and how many the project has in all (when the count answered). */
export interface TaskPage {
  tasks: Task[];
  total: number | undefined;
}

export class ProjectData extends Model {
  readonly agents: Live<Agent[]>;
  readonly rooms: Live<Room[]>;
  readonly unread: Live<Unread[]>;
  readonly needs: Live<NeedsYouItem[]>;
  readonly presence: Live<PresenceState[]>;
  readonly tasks: Live<TaskPage>;
  #id: string | undefined;
  #all: Live<unknown>[];

  constructor(client: Client, store: EventStore, private workspace: Workspace) {
    super();
    const pid = () => this.#id!;
    const mine = (e: { projectId?: string }) => !e.projectId || e.projectId === this.#id;
    this.agents = new Live(store, { load: () => client.agentList(pid()), when: (e) => starts(e, 'agent.') && mine(e) });
    this.rooms = new Live(store, { load: () => client.roomList(pid()), when: (e) => starts(e, 'room.created', 'room.archived', 'room.member', 'room.kind', 'room.mode') });
    this.unread = new Live(store, { load: () => client.roomUnread(pid()), when: (e) => starts(e, 'room.message_posted', 'room.read_cursor', 'room.created', 'room.archived') });
    this.needs = new Live(store, {
      load: () => client.needsYouList(pid()),
      when: needsChanged,
    });
    this.presence = new Live(store, { load: () => client.presenceList(), when: (e) => starts(e, 'room.presence_changed', 'agent.', 'watchdog.') });
    this.tasks = new Live(store, { load: () => this.#readTasks(client, pid()), when: (e) => starts(e, 'task.') && mine(e) });
    this.#all = [this.agents, this.rooms, this.unread, this.needs, this.presence, this.tasks] as Live<unknown>[];
    for (const l of this.#all) l.addEventListener('change', () => this.changed());
    workspace.addEventListener('change', () => this.#sync());
  }

  /**
   * The list and the count in one read, so what a screen says about "N of M" is one moment's answer. The count failing (or being
   * missing in a core that has no task.count) leaves the total unknown; the list is shown all the same.
   */
  async #readTasks(client: Client, projectId: string): Promise<TaskPage> {
    const count = async (): Promise<number | undefined> => {
      try {
        return (await client.taskCount(projectId)).total;
      } catch {
        return undefined;
      }
    };
    const [tasks, total] = await Promise.all([client.taskList(projectId), count()]);
    return { tasks, total };
  }

  start(signal?: AbortSignal): this {
    for (const l of this.#all) l.start(signal);
    this.#sync();
    return this;
  }

  /** Follow the workspace's selected project. */
  #sync(): void {
    const id = this.workspace.projectId;
    if (id === this.#id) return;
    this.#id = id;
    for (const l of this.#all) {
      l.reset(!!id);
      if (id) l.reload();
    }
    this.changed();
  }

  get id(): string | undefined {
    return this.#id;
  }

  get agentList(): Agent[] {
    return this.agents.data ?? [];
  }

  get roomList(): Room[] {
    return this.rooms.data ?? [];
  }

  /** The newest tasks the core lists (at most TASK_LIST_LIMIT), newest first. */
  get taskList(): Task[] {
    return this.tasks.data?.tasks ?? [];
  }

  /** How many tasks the project has in all; undefined until read, or when the count could not be. */
  get taskTotal(): number | undefined {
    return this.tasks.data?.total;
  }

  /** Whether `taskList` is every task of the project, or only the newest of them (M21): a screen that shows it as the whole says so. */
  get taskCoverage(): TaskCoverage {
    return taskCoverage(this.taskList.length, this.taskTotal);
  }

  get needsCount(): number {
    return this.needs.data?.length ?? 0;
  }
}
