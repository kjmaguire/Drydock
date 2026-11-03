// The window's shell (internal/core/ext_shell.go, audit 2026-10-16, M23): what the page cannot do for itself inside the desktop
// window. Like the rest of the client: one function per method in the core registry.

import type { Transport } from './transport.ts';

export interface ShellClient {
  /**
   * Asks the shell to open a web (http, https) or mail (mailto) address in the person's own browser or mail program. The core
   * refuses any other address, a web address with a password in it, and a window that cannot open another program, each with a
   * message that does not repeat the address. Resolves when the shell has handed the address over.
   */
  shellOpenExternal(url: string): Promise<void>;
  /** Starts Drydock again and closes this window, so a staged restore is applied now (decision P116). Only the desktop window can. */
  shellRestart(): Promise<void>;
  /**
   * What can open a task's worktree or a project's repository outside Drydock (effort review X26): whether this window can at all,
   * and the code editors found on this computer (an allowlist the core keeps).
   */
  shellEditors(): Promise<EditorList>;
  /**
   * Opens a task's worktree or a project's repository, or a file in it (a path relative to that folder), in Explorer or one of
   * shellEditors' editors. The core resolves and confines the path; only a person may.
   */
  shellOpenPath(p: OpenPathReq): Promise<void>;
  /** The third-party notices the build ships (THIRD_PARTY_NOTICES.md beside the executable), for Settings > About (decision P129). */
  appLicenses(): Promise<Licenses>;
}

/** core.EditorList */
export interface EditorList {
  available: boolean;
  editors: { id: string; name: string }[];
}

/** core.OpenPathReq: a task or a project, a path inside its folder, and Explorer or an editor's id. */
export interface OpenPathReq {
  taskId?: string;
  projectId?: string;
  path?: string;
  with: 'explorer' | 'editor';
  editor?: string;
}

/** core.Licenses: shipped with its text, or a note that says why there is none (a build from the source tree). */
export interface Licenses {
  shipped: boolean;
  file: string;
  text?: string;
  note?: string;
}

export function createShellClient(t: Transport): ShellClient {
  return {
    shellOpenExternal: async (url) => {
      await t.call('shell.openExternal', { url });
    },
    shellRestart: async () => {
      await t.call('shell.restart', {});
    },
    shellEditors: async () => {
      const v = await t.call<Partial<EditorList> | null>('shell.editors', {});
      return { available: !!v?.available, editors: Array.isArray(v?.editors) ? v.editors : [] };
    },
    shellOpenPath: async (p) => {
      await t.call('shell.openPath', p);
    },
    appLicenses: async () => {
      const v = await t.call<Partial<Licenses> | null>('app.licenses', {});
      return { shipped: !!v?.shipped, file: v?.file ?? '', text: v?.text ?? '', note: v?.note ?? '' };
    },
  };
}
