// The WSL run mode (internal/core/ext_wsl.go, decision P41): the distros on this machine, and a project's default run mode.
// Like the rest of the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';

/** core.WSLDistro: one distro as the agent forms list it, with what the last probe found (Connections probes). */
export interface WSLDistroInfo {
  name: string;
  default?: boolean;
  state?: string;
  version?: number;
  /** Where claude is inside the distro; empty when it was not found or not probed yet. */
  claude?: string;
  probed: boolean;
  signedIn?: boolean;
  interop: boolean;
  mountRoot?: string;
}

/** core.WSLStatus: whether WSL is on this machine, its user distros, and why there are none. */
export interface WSLStatus {
  available: boolean;
  distros: WSLDistroInfo[];
  message?: string;
}

/** core.ProjectRunMode: where the project's agents run unless they choose. */
export interface ProjectRunMode {
  runMode: 'native' | 'wsl' | string;
  wslDistro?: string;
  /** The person who set it, or "drydock" for a new project's default taken from the Connections (decision P58). */
  by?: string;
  at?: number;
  /** Why Drydock chose it (by "drydock"). */
  why?: string;
}

export interface WSLClient {
  wslDistros(): Promise<WSLStatus>;
  projectRunMode(projectId: string): Promise<ProjectRunMode>;
  /** Every project's default by project id, in one call: what the Agents page's "Used by" column asks for. */
  projectRunModes(): Promise<Record<string, ProjectRunMode>>;
  projectSetRunMode(projectId: string, runMode: string, wslDistro?: string): Promise<ProjectRunMode>;
}

export function createWSLClient(t: Transport): WSLClient {
  return {
    wslDistros: async () => {
      const s = await t.call<WSLStatus | null>('wsl.distros');
      return { available: s?.available === true, distros: Array.isArray(s?.distros) ? s.distros : [], message: s?.message };
    },
    projectRunMode: (projectId) => t.call('project.runMode', { projectId }),
    // A workspace without a project answers an empty object; a null from an older core is read the same way.
    projectRunModes: async () => {
      const m = await t.call<Record<string, ProjectRunMode> | null>('project.runModes');
      return m ?? {};
    },
    projectSetRunMode: (projectId, runMode, wslDistro = '') => t.call('project.setRunMode', { projectId, runMode, wslDistro }),
  };
}
