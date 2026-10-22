// The workspace: the projects, which one is selected (remembered in this browser), the license state and the app info.
// The shell and the screens read the selected project from here. It also reads what the Inbox and the sidebar count across
// every project (decision P97): what needs the person, the tasks that run and the security scans in progress.

import type { Client } from '../api/client.ts';
import type { LicenseView } from '../api/pkgTypes.ts';
import type { RunningScan } from '../api/securityTypes.ts';
import type { AppInfo, DrydockEvent, NeedsYouItem, Project, TaskSummaries, TaskSummary } from '../api/types.ts';
import type { EventStore } from './eventStore.ts';
import { Live, starts } from './live.ts';
import { needsChanged } from './project.ts';
import { Model } from './services.tsx';

/** The statuses of a task an agent works on now: the Inbox's Running tab and the sidebar's Running count. */
export const RUNNING_STATUSES: readonly string[] = ['CLAIMED', 'IN_PROGRESS', 'REVIEW', 'VERIFICATION'];

/** The events after which the open tasks' summaries (their step, the agent's doing line) may have changed. */
export const summariesChanged = (e: DrydockEvent): boolean => starts(e, 'task.', 'run.', 'room.presence_changed', 'workflow.', 'evidence.', 'review.');

const PROJECT_KEY = 'drydock.project';

function readProject(): string | undefined {
  try {
    return localStorage.getItem(PROJECT_KEY) ?? undefined;
  } catch {
    return undefined;
  }
}

export interface LicenseInfo {
  view: LicenseView | undefined;
  /** True only when licensing is configured in this build and the state is none or expired. A build without keys is never read-only. */
  readOnly: boolean;
  /** The core's own words for why work is turned off; empty when nothing is. */
  reason: string;
}

export function licenseInfo(view: LicenseView | undefined): LicenseInfo {
  const readOnly = !!view && view.configured && view.readOnly;
  return { view, readOnly, reason: readOnly ? view?.banner || view?.message || 'Drydock is read-only.' : '' };
}

export class Workspace extends Model {
  readonly projects: Live<Project[]>;
  readonly licenseLive: Live<LicenseView>;
  readonly info: Live<AppInfo>;
  /** Every project's items that need the person (needsyou.list with no project). */
  readonly needs: Live<NeedsYouItem[]>;
  /** Every project's open tasks and those that ended this week, as cards show them (task.summaries with no project). */
  readonly summaries: Live<TaskSummaries>;
  /** Every security scan in progress, of a project or of a folder. */
  readonly scans: Live<RunningScan[]>;
  #chosen: string | undefined;

  constructor(client: Client, store: EventStore, opts: { persist?: boolean } = {}) {
    super();
    this.#chosen = opts.persist === false ? undefined : readProject();
    this.projects = new Live(store, { load: () => client.projectList(), when: (e) => starts(e, 'project.') });
    this.licenseLive = new Live(store, { load: () => client.licenseStatus(), when: (e) => starts(e, 'license.') });
    this.info = new Live(store, { load: () => client.appInfo() });
    this.needs = new Live(store, { load: () => client.needsYouList(''), when: (e) => needsChanged(e) || starts(e, 'run.') });
    this.summaries = new Live(store, { load: () => client.taskSummaries({ projectId: '' }), when: summariesChanged });
    this.scans = new Live(store, { load: () => client.securityScanRunning(), when: (e) => starts(e, 'security.scan.') });
    for (const l of [this.projects, this.licenseLive, this.info, this.needs, this.summaries, this.scans] as Live<unknown>[]) l.addEventListener('change', () => this.changed());
  }

  start(signal?: AbortSignal): this {
    for (const l of [this.projects, this.licenseLive, this.info, this.needs, this.summaries, this.scans] as Live<unknown>[]) l.start(signal).reload();
    return this;
  }

  /** How many items need the person in every project. */
  get needsCount(): number {
    return this.needs.data?.length ?? 0;
  }

  /** The tasks an agent works on now, in every project. */
  get runningTasks(): TaskSummary[] {
    return (this.summaries.data?.summaries ?? []).filter((x) => RUNNING_STATUSES.includes(x.status));
  }

  /** The security scans in progress. */
  get runningScans(): RunningScan[] {
    return this.scans.data ?? [];
  }

  /** What runs now: the working tasks and the scans in progress (the sidebar's Running count). */
  get runningCount(): number {
    return this.runningTasks.length + this.runningScans.length;
  }

  /** A project's key ("GOC") by its id; empty when unknown. */
  projectKey(id: string | undefined): string {
    return (id && this.projectList.find((p) => p.id === id)?.key) || '';
  }

  get projectList(): Project[] {
    return this.projects.data ?? [];
  }

  /** The selected project: the remembered one if it still exists, else the first. */
  get project(): Project | undefined {
    const list = this.projectList;
    return list.find((p) => p.id === this.#chosen) ?? list[0];
  }

  get projectId(): string | undefined {
    return this.project?.id;
  }

  selectProject(id: string): void {
    this.#chosen = id;
    try {
      localStorage.setItem(PROJECT_KEY, id);
    } catch {
      /* optional convenience */
    }
    this.changed();
  }

  get license(): LicenseInfo {
    return licenseInfo(this.licenseLive.data);
  }

  #setupOpen = false;

  /**
   * True while the new-project wizard shows the result of a setup (its warnings) and the person has not opened the
   * project yet. On the first run the shell keeps the wizard on screen meanwhile, although a project now exists.
   */
  get setupOpen(): boolean {
    return this.#setupOpen;
  }

  setSetupOpen(v: boolean): void {
    if (this.#setupOpen === v) return;
    this.#setupOpen = v;
    this.changed();
  }
}
