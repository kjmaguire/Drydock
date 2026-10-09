// The selected project's shared reads: agents, rooms, unread counts, Needs You, presence and tasks. The shell, the details
// panel, the palette and most screens need them, so they are read once here and re-read on the events that change them.

import type { Client } from '../api/client.ts';
import type { Agent, NeedsYouItem, PresenceState, Room, Task, Unread } from '../api/types.ts';
import type { EventStore } from './eventStore.ts';
import { Live, starts } from './live.ts';
import { Model } from './services.tsx';
import type { Workspace } from './workspace.ts';

export class ProjectData extends Model {
  readonly agents: Live<Agent[]>;
  readonly rooms: Live<Room[]>;
  readonly unread: Live<Unread[]>;
  readonly needs: Live<NeedsYouItem[]>;
  readonly presence: Live<PresenceState[]>;
  readonly tasks: Live<Task[]>;
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
      when: (e) => starts(e, 'approval.', 'workflow.request', 'task.', 'watchdog.', 'room.message_posted', 'room.archived'),
    });
    this.presence = new Live(store, { load: () => client.presenceList(), when: (e) => starts(e, 'room.presence_changed', 'agent.', 'watchdog.') });
    this.tasks = new Live(store, { load: () => client.taskList(pid()), when: (e) => starts(e, 'task.') && mine(e) });
    this.#all = [this.agents, this.rooms, this.unread, this.needs, this.presence, this.tasks] as Live<unknown>[];
    for (const l of this.#all) l.addEventListener('change', () => this.changed());
    workspace.addEventListener('change', () => this.#sync());
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

  get taskList(): Task[] {
    return this.tasks.data ?? [];
  }

  get needsCount(): number {
    return this.needs.data?.length ?? 0;
  }
}
