// The workspace: the projects, which one is selected (remembered in this browser), the license state and the app info.
// The shell and the screens read the selected project from here.

import type { Client } from '../api/client.ts';
import type { LicenseView } from '../api/pkgTypes.ts';
import type { AppInfo, Project } from '../api/types.ts';
import type { EventStore } from './eventStore.ts';
import { Live, starts } from './live.ts';
import { Model } from './services.tsx';

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
  #chosen: string | undefined;

  constructor(client: Client, store: EventStore, opts: { persist?: boolean } = {}) {
    super();
    this.#chosen = opts.persist === false ? undefined : readProject();
    this.projects = new Live(store, { load: () => client.projectList(), when: (e) => starts(e, 'project.') });
    this.licenseLive = new Live(store, { load: () => client.licenseStatus(), when: (e) => starts(e, 'license.') });
    this.info = new Live(store, { load: () => client.appInfo() });
    for (const l of [this.projects, this.licenseLive, this.info]) l.addEventListener('change', () => this.changed());
  }

  start(signal?: AbortSignal): this {
    this.projects.start(signal).reload();
    this.licenseLive.start(signal).reload();
    this.info.start(signal).reload();
    return this;
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
