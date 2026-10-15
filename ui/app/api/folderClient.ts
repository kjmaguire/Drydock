// A project's folder (internal/core/ext_folders.go, decision P58): the window's folder dialog, what is at a folder, and
// making a Git repository there. Like the rest of the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';

/** runners.Drive: a Dev Drive ("D:"). */
export interface DevDrive {
  letter: string;
  trusted: boolean;
}

/** core.FolderHints: whether the window has a folder dialog, whether this is Windows, and the Dev Drives found. */
export interface FolderHints {
  canPick: boolean;
  windows: boolean;
  devDrives: DevDrive[];
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
  fsPickFolder(title?: string): Promise<string>;
  fsFolderHints(): Promise<FolderHints>;
  repoCheck(path: string): Promise<RepoFolder>;
  /** Makes the folder (when missing) a Git repository on main with a first commit. Only a person may. */
  repoInit(path: string): Promise<RepoInit>;
}

export function createFolderClient(t: Transport): FolderClient {
  return {
    fsPickFolder: async (title = '') => (await t.call<{ path?: string } | null>('fs.pickFolder', { title }))?.path ?? '',
    fsFolderHints: async () => {
      const h = await t.call<FolderHints | null>('fs.folderHints', {});
      return { canPick: h?.canPick === true, windows: h?.windows === true, devDrives: Array.isArray(h?.devDrives) ? h.devDrives : [] };
    },
    repoCheck: (path) => t.call('repo.check', { path }),
    repoInit: (path) => t.call('repo.init', { path }),
  };
}
