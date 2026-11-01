// Project setup: the new-project wizard (project.setup), projects without a repository (attachRepo, setBrief), the user's
// GitHub account through their own `gh` (status, owners, repos, clone), the built-in MCP catalog and the prebuilt agents.
// Like the rest of the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';
import type {
  CloneResult,
  GitHubRepo,
  GitHubStatus,
  McpCatalogAdded,
  McpCatalogEntry,
  PrebuiltAgent,
  PrebuiltApplied,
  PrebuiltApplyReq,
  Project,
  ProjectRepos,
  RepoSync,
  RepoSyncSettings,
  SetupRequest,
  SetupResult,
} from './types.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export interface SetupClient {
  /** Validates the whole request before creating anything; what fails after the project exists comes back as warnings. */
  projectSetup(req: SetupRequest): Promise<SetupResult>;
  /** Once per project: a repository is never swapped. */
  projectAttachRepo(id: string, repoPath: string): Promise<Project>;
  projectSetBrief(id: string, brief: string): Promise<Project>;
  githubStatus(): Promise<GitHubStatus>;
  /** The signed-in account first, then its organizations. */
  githubOwners(): Promise<string[]>;
  githubRepos(owner?: string, query?: string): Promise<GitHubRepo[]>;
  /** Where a clone starts: the folder the person last cloned into, else the usual one (~/source/repos; P143). */
  githubCloneDir(): Promise<string>;
  githubClone(repo: string, dir: string): Promise<CloneResult>;
  /**
   * Every project with its repository, the CI state of its default branch and its open pull requests (the GitHub page's
   * Repositories table). Answers are kept a minute; `refresh` asks GitHub again. A failure is the row's, not the call's.
   */
  githubProjectRepos(refresh?: boolean): Promise<ProjectRepos>;
  /**
   * Where the project's folder stands against its origin remote, from the last fetch (P143). Reading it when the last check is old
   * (and the background check is on) starts a fetch-only check: `checking` is true and a repo.synced event follows.
   */
  repoSyncState(projectId: string): Promise<RepoSync>;
  /** Sync now: fetch, then fast-forward the checked-out default branch when nothing else changes (outcome says what it did). */
  repoSync(projectId: string): Promise<RepoSync>;
  repoSyncSettings(): Promise<RepoSyncSettings>;
  repoSyncSettingsSet(p: Partial<RepoSyncSettings>): Promise<RepoSyncSettings>;
  mcpCatalog(): Promise<McpCatalogEntry[]>;
  mcpAddFromCatalog(catalogId: string): Promise<McpCatalogAdded>;
  agentCatalogList(projectId?: string): Promise<PrebuiltAgent[]>;
  agentCatalogApply(req: PrebuiltApplyReq): Promise<PrebuiltApplied>;
}

const prebuilt = (e: PrebuiltAgent): PrebuiltAgent => ({
  ...e,
  alternatives: arr(e.alternatives),
  mcp: arr(e.mcp),
  skills: arr(e.skills),
  tags: arr(e.tags),
  availability: {
    adapters: arr(e.availability?.adapters),
    preferredUsable: !!e.availability?.preferredUsable,
    mcp: arr(e.availability?.mcp),
    skills: arr(e.availability?.skills),
  },
});

export function createSetupClient(t: Transport): SetupClient {
  return {
    projectSetup: async (req) => {
      const r = await t.call<SetupResult>('project.setup', req);
      return { ...r, agents: arr(r.agents), warnings: arr(r.warnings) };
    },
    projectAttachRepo: (id, repoPath) => t.call('project.attachRepo', { id, repoPath }),
    projectSetBrief: (id, brief) => t.call('project.setBrief', { id, brief }),
    githubStatus: () => t.call('github.status', {}),
    githubOwners: async () => arr(await t.call<string[] | null>('github.owners', {})),
    githubRepos: async (owner = '', query = '') => arr(await t.call<GitHubRepo[] | null>('github.repos', { owner, query })),
    githubCloneDir: async () => (await t.call<string | null>('github.cloneDir', {})) ?? '',
    githubClone: (repo, dir) => t.call('github.clone', { repo, dir }),
    githubProjectRepos: async (refresh = false) => {
      const r = await t.call<ProjectRepos>('github.projectRepos', { refresh });
      return { ghInstalled: !!r?.ghInstalled, rows: arr(r?.rows).map((x) => ({ ...x, openPullRequests: Number(x.openPullRequests) || 0 })) };
    },
    repoSyncState: (projectId) => t.call('repo.syncState', { projectId }),
    repoSync: (projectId) => t.call('repo.sync', { projectId }),
    repoSyncSettings: () => t.call('repo.syncSettings.get', {}),
    repoSyncSettingsSet: (p) => t.call('repo.syncSettings.set', p),
    mcpCatalog: async () => arr(await t.call<McpCatalogEntry[] | null>('mcplib.catalog', {})).map((e) => ({ ...e, readOnlyTools: arr(e.readOnlyTools), recommendedRoles: arr(e.recommendedRoles) })),
    mcpAddFromCatalog: (catalogId) => t.call('mcplib.addFromCatalog', { catalogId }),
    agentCatalogList: async (projectId) => arr(await t.call<PrebuiltAgent[] | null>('agentcatalog.list', projectId ? { projectId } : {})).map(prebuilt),
    agentCatalogApply: async (req) => {
      const r = await t.call<PrebuiltApplied>('agentcatalog.apply', req);
      return {
        ...r,
        mcp: { granted: arr(r.mcp?.granted), skipped: arr(r.mcp?.skipped) },
        skills: { assigned: arr(r.skills?.assigned), shared: arr(r.skills?.shared), pending: arr(r.skills?.pending), failed: arr(r.skills?.failed) },
        notes: arr(r.notes),
      };
    },
  };
}
