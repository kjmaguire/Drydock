// The new-project wizard's state and rules, kept apart from the components so they can be tested in Node: which step is
// complete, the request sent to project.setup, the suggested team, the agent CLI choices of a prebuilt agent.

import {
  PROJECT_BRIEF_MAX,
  SETUP_MAX_AGENTS,
  type AdapterInfo,
  type AgentCategory,
  type GitHubRepo,
  type PrebuiltAgent,
  type ProjectMode,
  type SetupAgent,
  type SetupRequest,
} from '../../api/types.ts';
import type { FolderHints, RepoFolder } from '../../api/folderClient.ts';

export const STEPS = ['Basics', 'Repository', 'Connectors', 'Team', 'Review'] as const;
export type Step = (typeof STEPS)[number];

export type RepoChoice = 'github' | 'local' | 'none';

export interface TeamMember {
  /** Stable key for the list (never sent). */
  key: string;
  source: 'prebuilt' | 'template' | 'custom';
  /** The catalog entry id or template id. */
  ref?: string;
  name: string;
  role: string;
  adapter: string;
  model: string;
  effort: string;
  permissionMode: string;
  purpose: string;
  /** For display: the catalog category, skills and MCP counts. */
  category?: string;
  skills?: number;
  mcp?: string[];
}

export interface WizardState {
  name: string;
  brief: string;
  repo: RepoChoice;
  localPath: string;
  /** What repo.check found at localPath (undefined until it answered; its path says for which text). */
  localCheck: RepoFolder | undefined;
  /** Make a Git repository at localPath when the project is created (the folder is not one yet; decision P58). */
  initRepo: boolean;
  github: GitHubRepo | undefined;
  cloneDir: string;
  mode: ProjectMode;
  libraryServers: string[];
  catalogServers: string[];
  team: TeamMember[];
  installSkills: boolean;
  /** The default team was offered once; removing it must not bring it back. */
  suggested: boolean;
}

export function emptyWizard(): WizardState {
  return {
    name: '',
    brief: '',
    repo: 'local',
    localPath: '',
    localCheck: undefined,
    initRepo: false,
    github: undefined,
    cloneDir: '',
    mode: 'local_merge',
    libraryServers: [],
    catalogServers: [],
    team: [],
    installSkills: true,
    suggested: false,
  };
}

export const CATEGORY_LABEL: Record<AgentCategory, string> = {
  management: 'Management',
  engineering: 'Engineering',
  design: 'Design',
  docs: 'Docs',
  quality: 'Quality',
  security: 'Security',
};

/** The folder separator the clone folder already uses (Windows paths use a backslash). */
function sepOf(dir: string): string {
  return dir.includes('\\') || /^[A-Za-z]:/.test(dir) ? '\\' : '/';
}

/** Where a GitHub repository will be cloned: the clone folder joined with the repository name. */
export function cloneTarget(cloneDir: string, repo: Pick<GitHubRepo, 'name'> | undefined): string {
  const dir = cloneDir.trim();
  if (!dir || !repo) return '';
  const sep = sepOf(dir);
  return dir.replace(/[\\/]+$/, '') + sep + repo.name;
}

/** The delivery mode that can apply: pull requests need a GitHub repository; no repository delivers nothing. */
export function effectiveMode(s: Pick<WizardState, 'repo' | 'mode'>): ProjectMode {
  return s.repo === 'github' && s.mode === 'pr_flow' ? 'pr_flow' : 'local_merge';
}

/** The check of the folder as it is typed now (a check of an earlier text does not count). */
export function currentCheck(s: Pick<WizardState, 'localPath' | 'localCheck'>): RepoFolder | undefined {
  const c = s.localCheck;
  return c && c.path === s.localPath.trim().replace(/^"+|"+$/g, '').trim() ? c : undefined;
}

/**
 * What the folder check says keeps a local folder from being used, in words; undefined when the folder is a repository, the
 * person asked Drydock to make one there, or the check has not answered (the core checks again when the project is made).
 */
export function folderProblem(check: RepoFolder | undefined, initRepo: boolean): string | undefined {
  if (!check) return undefined;
  switch (check.state) {
    case 'repository':
      return undefined;
    case 'folder':
    case 'missing':
      return initRepo ? undefined : `${check.state === 'missing' ? 'This folder does not exist yet' : 'This folder is not a Git repository'}: tick "Create a Git repository here", or choose another folder.`;
    default:
      return check.message;
  }
}

/** The hint under the path: the Dev Drive advice on Windows (with the Dev Drives found), else what the field takes. */
export function folderHint(h: FolderHints | undefined): string {
  const base = 'A folder with a Git repository, or a new folder: Drydock can make the repository.';
  if (!h || (!h.windows && h.devDrives.length === 0)) return base;
  const found = h.devDrives.length
    ? ` Dev Drive ${h.devDrives.map((d) => d.letter).join(', ')} found.`
    : h.devDriveReason
      ? ` No Dev Drive was found on this PC: ${h.devDriveReason}.`
      : ' No Dev Drive was found on this PC.';
  return `${base} Keeping projects on a Windows Dev Drive is recommended: faster builds, and Microsoft Defender's performance mode.${found}`;
}

/** What keeps a step from being complete, in words; undefined when it is complete. */
export function stepProblem(s: WizardState, step: Step): string | undefined {
  switch (step) {
    case 'Basics':
      if (s.brief.trim().length > PROJECT_BRIEF_MAX) return `The brief is at most ${PROJECT_BRIEF_MAX} characters.`;
      return undefined;
    case 'Repository':
      if (s.repo === 'local' && !s.localPath.trim()) return 'Enter the folder of the local repository.';
      if (s.repo === 'local') {
        const p = folderProblem(currentCheck(s), s.initRepo);
        if (p) return p;
      }
      if (s.repo === 'github' && !s.github) return 'Choose a GitHub repository.';
      if (s.repo === 'github' && !s.cloneDir.trim()) return 'Choose the folder to clone into.';
      if (s.repo === 'none' && !s.name.trim()) return 'A project without a repository needs a name (go back to Basics).';
      return undefined;
    case 'Connectors':
      return undefined;
    case 'Team': {
      if (s.team.length > SETUP_MAX_AGENTS) return `At most ${SETUP_MAX_AGENTS} agents in one go: add more from the Team page afterwards.`;
      const bad = s.team.find((m) => !m.name.trim() || (m.source === 'custom' && (!m.role.trim() || !m.adapter)));
      if (bad) return bad.name.trim() ? `${bad.name} needs a role and an agent CLI.` : 'Every agent needs a name.';
      const names = s.team.map((m) => m.name.trim().toLowerCase());
      const dup = names.find((n, i) => names.indexOf(n) !== i);
      if (dup) return `Two agents are called "${s.team[names.indexOf(dup)]!.name.trim()}": give each a different name.`;
      return undefined;
    }
    case 'Review':
      for (const st of STEPS) {
        if (st === 'Review') break;
        const p = stepProblem(s, st);
        if (p) return `${st}: ${p}`;
      }
      return undefined;
  }
}

/** The first step that is not complete (Review when all are). */
export function firstIncomplete(s: WizardState): Step {
  return STEPS.find((st) => st !== 'Review' && stepProblem(s, st)) ?? 'Review';
}

/** A step can be opened when every step before it is complete. */
export function canOpen(s: WizardState, step: Step): boolean {
  const i = STEPS.indexOf(step);
  return STEPS.slice(0, i).every((st) => !stepProblem(s, st));
}

/** The request for project.setup. */
export function buildSetup(s: WizardState): SetupRequest {
  const repo: SetupRequest['repo'] =
    s.repo === 'local'
      ? { kind: 'local', path: s.localPath.trim(), ...(s.initRepo ? { init: true } : {}) }
      : s.repo === 'github' && s.github
        ? { kind: 'github', github: s.github.fullName, cloneDir: s.cloneDir.trim() }
        : { kind: 'none' };
  const agents: SetupAgent[] = s.team.map((m) => {
    const common = { name: m.name.trim(), adapter: m.adapter, model: m.model.trim(), effort: m.effort, permissionMode: m.permissionMode };
    if (m.source === 'prebuilt') return { prebuilt: m.ref, ...common };
    if (m.source === 'template') return { template: m.ref, name: m.name.trim() };
    return { ...common, role: m.role.trim(), purpose: m.purpose.trim() };
  });
  return {
    name: s.name.trim(),
    brief: s.brief.trim(),
    repo,
    mode: effectiveMode(s),
    libraryServers: [...s.libraryServers],
    catalogServers: [...s.catalogServers],
    agents,
    installSkills: s.installSkills,
  };
}

let keySeq = 0;
export const memberKey = (): string => `m${++keySeq}`;

/** A name not yet used in the team: "Team Lead", then "Team Lead 2", ... */
export function uniqueName(base: string, team: readonly Pick<TeamMember, 'name'>[]): string {
  const taken = new Set(team.map((m) => m.name.trim().toLowerCase()));
  if (!taken.has(base.toLowerCase())) return base;
  for (let i = 2; ; i++) if (!taken.has(`${base} ${i}`.toLowerCase())) return `${base} ${i}`;
}

/** The model and effort a prebuilt agent uses on an adapter: its preferred setup, or the listed alternative's. */
export function setupFor(e: PrebuiltAgent, adapter: string): { model: string; effort: string } {
  if (adapter === e.preferred.adapter) return { model: e.preferred.model ?? '', effort: e.preferred.effort ?? '' };
  const alt = e.alternatives.find((a) => a.adapter === adapter);
  return { model: alt?.model ?? '', effort: alt?.effort ?? '' };
}

/** A team member from a prebuilt agent, on its preferred CLI (or the first usable alternative when that is not usable). */
export function memberFromPrebuilt(e: PrebuiltAgent, team: readonly TeamMember[]): TeamMember {
  const usable = e.availability.adapters.find((a) => a.usable)?.adapter;
  const adapter = e.availability.preferredUsable || !usable ? e.preferred.adapter : usable;
  const { model, effort } = setupFor(e, adapter);
  return {
    key: memberKey(),
    source: 'prebuilt',
    ref: e.id,
    name: uniqueName(e.name, team),
    role: e.role,
    adapter,
    model,
    effort,
    permissionMode: adapter === e.preferred.adapter ? (e.preferred.permissionMode ?? '') : '',
    purpose: e.purpose,
    category: e.category,
    skills: e.skills.length,
    mcp: e.mcp,
  };
}

/** The default team the first time: a Team Lead, one engineer and a Code Reviewer (whichever of them the catalog has). */
export const SUGGESTED = ['team-lead', 'backend-engineer', 'code-reviewer'];
export function suggestTeam(catalog: readonly PrebuiltAgent[]): TeamMember[] {
  const team: TeamMember[] = [];
  for (const id of SUGGESTED) {
    const e = catalog.find((x) => x.id === id);
    if (e) team.push(memberFromPrebuilt(e, team));
  }
  return team;
}

export interface AdapterChoice {
  id: string;
  label: string;
  disabled: boolean;
  reason?: string;
  /** Not usable on Windows but ready in this WSL distro: the agent runs there (decision P58). */
  wslDistro?: string;
}

/**
 * The agent CLIs to offer for a member: a prebuilt agent's preferred CLI and alternatives first, then the others. A CLI
 * that is not installed (or not usable) is offered disabled with the reason, except the one already chosen. A CLI installed
 * only inside a WSL distro counts as available and names the distro.
 */
export function adapterChoices(adapters: readonly AdapterInfo[], entry: PrebuiltAgent | undefined, current: string): AdapterChoice[] {
  const first = entry ? [entry.preferred.adapter, ...entry.alternatives.map((a) => a.adapter)] : [];
  const ids = [...new Set([...first, ...adapters.map((a) => a.id), ...(current ? [current] : [])])];
  return ids.map((id) => {
    const info = adapters.find((a) => a.id === id);
    const avail = entry?.availability.adapters.find((a) => a.adapter === id);
    const wslDistro = avail ? avail.wslDistro : info && !info.detection.installed ? info.wslDistro : undefined;
    const installed = avail ? avail.installed : !!info?.detection.installed || !!wslDistro;
    const usable = avail
      ? avail.usable
      : info
        ? (info.detection.installed && info.detection.status !== 'not_installed' && info.detection.status !== 'unsupported_version') || !!wslDistro
        : false;
    const reason = !installed ? avail?.reason || 'not installed on this machine' : !usable ? avail?.reason || info?.detection.status : undefined;
    const tag = entry && id === entry.preferred.adapter ? ' (recommended)' : entry && first.includes(id) ? ' (alternative)' : '';
    const note = wslDistro && usable ? ` - in WSL ${wslDistro}` : reason ? ` - ${reason}` : '';
    return { id, label: `${id}${tag}${note}`, disabled: !usable && id !== current, reason, ...(wslDistro && usable ? { wslDistro } : {}) };
  });
}

/** "pushed 3 days ago" from a GitHub timestamp. */
export function pushedAgo(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const days = Math.floor((now - t) / 86_400_000);
  if (days <= 0) return 'pushed today';
  if (days === 1) return 'pushed yesterday';
  if (days < 30) return `pushed ${days} days ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `pushed ${months} ${months === 1 ? 'month' : 'months'} ago`;
  const years = Math.floor(days / 365);
  return `pushed ${years} ${years === 1 ? 'year' : 'years'} ago`;
}

/** The note shown for a catalog connector's sign-in. */
export function authNote(auth: string, optional: boolean): string {
  if (auth === 'oauth') return optional ? 'Works without signing in; sign in after adding for more.' : 'Needs sign-in after adding.';
  if (auth === 'bearer') return optional ? 'Works without a token; add one after adding for more.' : 'Needs a token after adding.';
  return 'No sign-in needed.';
}
