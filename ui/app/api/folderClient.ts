// A project's folder (internal/core/ext_folders.go, decision P58): the window's folder dialog, what is at a folder, and
// making a Git repository there. Like the rest of the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';

/** runners.Drive: a Dev Drive ("D:"). */
export interface DevDrive {
  letter: string;
  trusted: boolean;
}

/** core.FolderHints: whether the window has a folder dialog, whether this is Windows, the Dev Drives found, and why none was. */
export interface FolderHints {
  canPick: boolean;
  /** The window also has a file dialog (fs.pickFile). */
  canPickFile?: boolean;
  /** The window has a Save dialog (fs.pickSave, A11). */
  canPickSave?: boolean;
  /** The window can show a file in File Explorer (fs.reveal, A11). */
  canReveal?: boolean;
  windows: boolean;
  devDrives: DevDrive[];
  /** Why devDrives is empty on Windows: a clause after "No Dev Drive was found: ". */
  devDriveReason?: string;
}

/** core.RepoFolder.state. */
export type RepoFolderState = 'repository' | 'no_branch' | 'inside' | 'folder' | 'missing' | 'not_folder' | 'invalid';

/** core.RepoFolder: what is at a folder given for a project's repository. */
export interface RepoFolder {
  path: string;
  state: RepoFolderState;
  /** repository, no_branch, inside: the work tree's top folder. */
  root?: string;
  /** repository: the checked-out branch. */
  branch?: string;
  /** folder: the files and folders at its top. */
  entries: number;
  /** Drydock may make a repository here (folder, missing). */
  canInit: boolean;
  message: string;
}

/** core.RepoInit: what repo.init made. */
export interface RepoInit {
  path: string;
  branch: string;
  createdFolder: boolean;
  /** Files in the first commit. */
  files: number;
}

export interface FolderClient {
  /** Opens the window's folder dialog; '' when the person cancelled. Refused where the window has none. */
  /** The window's folder dialog, opened at dir (or the deepest part of it that exists; empty: the person's code folder). */
  fsPickFolder(title?: string, dir?: string): Promise<string>;
  fsFolderHints(): Promise<FolderHints>;
  /** Opens the window's file dialog for one kind of file Drydock reads ("" when the person cancelled). Only a person may. */
  fsPickFile(kind: 'project-archive' | 'license'): Promise<string>;
  /** Opens the window's Save dialog for a file Drydock writes, at the folder of `dir` ("" when the person cancelled). Only a person may. */
  fsPickSave(kind: 'support-bundle' | 'project-export', dir?: string): Promise<string>;
  /** Shows a file Drydock saved in this session in File Explorer, selected. */
  fsReveal(path: string): Promise<void>;
  repoCheck(path: string): Promise<RepoFolder>;
  /** Makes the folder (when missing) a Git repository on main with a first commit. Only a person may. */
  repoInit(path: string): Promise<RepoInit>;
}

export function createFolderClient(t: Transport): FolderClient {
  return {
    fsPickFolder: async (title = '', dir = '') => (await t.call<{ path?: string } | null>('fs.pickFolder', { title, dir }))?.path ?? '',
    fsPickFile: async (kind) => (await t.call<{ path?: string } | null>('fs.pickFile', { kind }))?.path ?? '',
    fsPickSave: async (kind, dir = '') => (await t.call<{ path?: string } | null>('fs.pickSave', { kind, dir }))?.path ?? '',
    fsReveal: async (path) => void (await t.call('fs.reveal', { path })),
    fsFolderHints: async () => {
      const h = await t.call<FolderHints | null>('fs.folderHints', {});
      return {
        canPick: h?.canPick === true,
        canPickFile: h?.canPickFile === true,
        canPickSave: h?.canPickSave === true,
        canReveal: h?.canReveal === true,
        windows: h?.windows === true,
        devDrives: Array.isArray(h?.devDrives) ? h.devDrives : [],
        ...(typeof h?.devDriveReason === 'string' && h.devDriveReason ? { devDriveReason: h.devDriveReason } : {}),
      };
    },
    repoCheck: (path) => t.call('repo.check', { path }),
    repoInit: (path) => t.call('repo.init', { path }),
  };
}
