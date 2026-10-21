// TEST DOUBLES (labelled): catalog entries, GitHub repositories and catalog connectors shaped like the core's answers, for
// the wizard, gallery and settings tests. Nothing here imitates an agent.

import type { AdapterInfo, GitHubRepo, McpCatalogEntry, PrebuiltAgent } from '../../api/types.ts';

const ENTRIES: Record<string, { name: string; role: string; category: string; model: string; effort: string; skills?: number; mcp?: string[] }> = {
  'team-lead': { name: 'Team Lead', role: 'lead', category: 'management', model: 'opus', effort: 'high' },
  'backend-engineer': { name: 'Backend Engineer', role: 'backend', category: 'engineering', model: 'sonnet', effort: 'medium' },
  'code-reviewer': { name: 'Code Reviewer', role: 'reviewer', category: 'quality', model: 'opus', effort: 'high' },
  'ui-engineer': { name: 'UI Engineer', role: 'frontend', category: 'design', model: 'sonnet', effort: 'medium', skills: 4 },
  'cloudflare-engineer': { name: 'Cloudflare Engineer', role: 'cloudflare', category: 'engineering', model: 'sonnet', effort: 'medium', skills: 4, mcp: ['cloudflare-docs'] },
  'technical-writer': { name: 'Technical Writer', role: 'docs', category: 'docs', model: 'sonnet', effort: 'medium', mcp: ['microsoft-learn'] },
};

/** A catalog entry; `avail` sets which CLIs are installed (default: all). */
export function prebuilt(id: string, avail: Record<string, boolean> = {}): PrebuiltAgent {
  const e = ENTRIES[id] ?? { name: id, role: id, category: 'engineering', model: '', effort: '' };
  const on = (a: string) => avail[a] ?? true;
  const adapters = ['claude', 'gemini', 'codex'].map((a, i) => ({
    adapter: a,
    preferred: i === 0,
    installed: on(a),
    usable: on(a),
    status: on(a) ? 'ready' : 'not_installed',
    reason: on(a) ? undefined : `${a} is not installed`,
  }));
  return {
    id,
    name: e.name,
    role: e.role,
    category: e.category,
    summary: `${e.name} summary.`,
    description: `${e.name} description.`,
    purpose: `You are the ${e.name}.`,
    preferred: { adapter: 'claude', model: e.model, effort: e.effort, permissionMode: '', practices: true },
    alternatives: [
      { adapter: 'gemini', model: 'gemini-2.5-pro', effort: '' },
      { adapter: 'codex', model: '', effort: '' },
    ],
    mcp: e.mcp ?? [],
    skills: skillRefs(e.skills ?? 0),
    tags: [],
    availability: {
      adapters,
      preferredUsable: on('claude'),
      mcp: (e.mcp ?? []).map((m) => ({ id: m, ...MCP_INFO[m], inLibrary: false })),
      skills: skillRefs(e.skills ?? 0).map((k, i) => ({
        ...k,
        description: `What skill ${i} does, from its SKILL.md.`,
        license: 'MIT',
        repo: 'acme/skills',
        sourceUrl: `https://github.com/acme/skills/tree/${k.ref}/skills/${k.name}`,
        imported: false,
        samePin: false,
      })),
    },
  };
}

const skillRefs = (n: number) => Array.from({ length: n }, (_, i) => ({ registry: 'r', name: `skill-${i}`, ref: '86505e44f87d801aa1864a3c4109e9a8ae5c409f' }));

const MCP_INFO: Record<string, { display: string; publisher: string; brand: string; summary: string; auth: string }> = {
  'cloudflare-docs': { display: 'Cloudflare Documentation', publisher: 'Cloudflare', brand: 'cloudflare', summary: 'Search the Cloudflare developer documentation.', auth: 'none' },
  'microsoft-learn': { display: 'Microsoft Learn', publisher: 'Microsoft', brand: 'microsoft', summary: 'Search official Microsoft documentation.', auth: 'none' },
};

export const CATALOG: PrebuiltAgent[] = Object.keys(ENTRIES).map((id) => prebuilt(id));

export function repo(fullName: string, over: Partial<GitHubRepo> = {}): GitHubRepo {
  const [owner, name] = fullName.split('/') as [string, string];
  return {
    fullName,
    owner,
    name,
    description: `${name} description`,
    private: false,
    archived: false,
    fork: false,
    defaultBranch: 'main',
    htmlUrl: `https://github.com/${fullName}`,
    pushedAt: '2026-10-01T00:00:00Z',
    ...over,
  };
}

export function catalogServer(id: string, over: Partial<McpCatalogEntry> = {}): McpCatalogEntry {
  return {
    id,
    display: id,
    url: `https://${id}.example/mcp`,
    description: `${id} docs`,
    auth: 'none',
    authOptional: false,
    readOnlyTools: [],
    recommendedRoles: [],
    trust: 'official',
    inLibrary: false,
    ...over,
  };
}

export function adapterInfo(id: string, installed = true, models: string[] = []): AdapterInfo {
  return {
    id,
    detection: { adapter: id, installed, status: installed ? 'ready' : 'not_installed' },
    capabilities: {
      streaming: true,
      structuredOutput: true,
      modelSelection: true,
      effortControl: true,
      sessionResume: true,
      interrupt: true,
      mcp: true,
      tokenUsage: true,
      costReporting: true,
      headless: true,
      imageInput: false,
      toolCallVisibility: true,
      permissionModes: ['default', 'acceptEdits', 'plan'],
      efforts: ['low', 'medium', 'high'],
      models,
    },
  };
}
