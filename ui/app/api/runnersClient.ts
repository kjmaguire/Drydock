// The CI runners methods (internal/core/ext_runners.go, decision P56): GitHub Actions self-hosted runners for the person's
// private repositories, kept by Drydock on this PC. Like the rest of the client: no behavior beyond turning Go's nil slices
// (JSON null) into empty arrays.

import type { Transport } from './transport.ts';

export type RunnerKindName = 'windows' | 'linux';
export type RunnerState = 'paused' | 'starting' | 'idle' | 'busy' | 'blocked' | 'error';

export interface RunnerKind {
  kind: RunnerKindName;
  available: boolean;
  reason?: string;
  /** Where its jobs run, in words. */
  where: string;
  freeBytes?: number;
  /** What a workflow writes after runs-on to use it. */
  runsOn: string;
}

export interface RunnerJobResult {
  name: string;
  result: string;
  at: string;
}

export interface RunnerSlot {
  key: string;
  repo: string;
  kind: RunnerKindName;
  enabled: boolean;
  state: RunnerState;
  reason?: string;
  runner?: string;
  job?: string;
  lastJob?: RunnerJobResult;
  jobs: number;
  version?: string;
  since: string;
  by?: string;
  at?: string;
}

export interface DevDrive {
  letter: string;
  trusted: boolean;
}

export interface RunnersView {
  kinds: RunnerKind[];
  root?: string;
  /** chosen | devdrive | default */
  rootSource?: string;
  devDrives: DevDrive[];
  slots: RunnerSlot[];
  minFreeBytes: number;
  max: number;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const view = (v: RunnersView): RunnersView => ({ ...v, kinds: arr(v.kinds), devDrives: arr(v.devDrives), slots: arr(v.slots) });

export interface RunnersClient {
  runnersGet(): Promise<RunnersView>;
  /** Adds a runner (private repositories only; a Windows one needs acceptRunsAsYou). Only a person may. */
  runnersAdd(repo: string, kind: RunnerKindName, acceptRunsAsYou: boolean): Promise<RunnersView>;
  /** Stops and unregisters the runner, and forgets it. */
  runnersRemove(repo: string, kind: RunnerKindName): Promise<RunnersView>;
  /** Switches a runner on or off; off stops and unregisters it. */
  runnersEnable(repo: string, kind: RunnerKindName, enabled: boolean): Promise<RunnersView>;
  /** Where Windows jobs run ('' goes back to the Dev Drive or the app's folder). Refused while a Windows runner is on. */
  runnersRootSet(path: string): Promise<RunnersView>;
  /** The end of a runner's output. */
  runnersLog(repo: string, kind: RunnerKindName): Promise<string[]>;
}

export function createRunnersClient(t: Transport): RunnersClient {
  return {
    runnersGet: async () => view(await t.call<RunnersView>('runners.get')),
    runnersAdd: async (repo, kind, acceptRunsAsYou) => view(await t.call<RunnersView>('runners.add', { repo, kind, acceptRunsAsYou })),
    runnersRemove: async (repo, kind) => view(await t.call<RunnersView>('runners.remove', { repo, kind })),
    runnersEnable: async (repo, kind, enabled) => view(await t.call<RunnersView>('runners.enable', { repo, kind, enabled })),
    runnersRootSet: async (path) => view(await t.call<RunnersView>('runners.root.set', { path })),
    runnersLog: async (repo, kind) => arr((await t.call<{ lines: string[] | null }>('runners.log', { repo, kind })).lines),
  };
}
