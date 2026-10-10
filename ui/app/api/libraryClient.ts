// Typed client for the agent templates and the agents' environment (internal/core/ext_agenttemplates.go, ext_agentenv.go).

import type { Agent, SkillRef, SkippedItem } from './types.ts';
import type { Transport } from './transport.ts';

/** A team member defined once in the workspace. Applying it makes an ordinary agent in a project; there is no link afterwards. */
export interface AgentTemplate {
  id: string;
  name: string;
  role: string;
  purpose: string;
  adapter: string;
  model: string;
  effort: string;
  permissionMode: string;
  providerId: string;
  /** Library MCP server ids the new agent is granted. */
  mcpServers: string[];
  /** Registry skills, pinned to a commit, installed in the project and given to the new agent. */
  skills: SkillRef[];
  /** Whether agents made from the template get the Claude best-practices text; omitted means yes. */
  practices?: boolean;
  createdAt: number;
  updatedAt: number;
}

/** A registry skill a template can carry: one a prebuilt agent comes with, or one imported into a project here. */
export interface SkillChoice extends SkillRef {
  description: string;
  /** Where Drydock knows it from: "prebuilt UI Engineer", "project gocalc". */
  from: string[];
}

/** What installing an agent's skills did. */
export interface SkillsInstalled {
  assigned: string[];
  shared: string[];
  pending: SkippedItem[];
  failed: SkippedItem[];
}

export type AgentTemplateInput = Omit<AgentTemplate, 'id' | 'createdAt' | 'updatedAt'> & { id?: string };

export interface AppliedTemplate {
  agent: Agent;
  granted: string[];
  /** A listed server that could not be granted, with the reason. */
  skipped: string[];
  skills: SkillsInstalled;
}

/** The working practices Drydock gives a Claude Code agent in a role, and a suggested model and effort for that role. */
export interface ClaudePractices {
  version: number;
  role: string;
  family: 'worker' | 'reviewer' | 'planner';
  text: string;
  preset: { model: string; effort: string; why: string };
}

export interface AgentEnv {
  /** Extra variable names the human chose to pass to agents. */
  pass: string[];
  /** Names every agent process keeps, and prefixes kept for Claude Code. */
  kept: string[];
  prefixes: string[];
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const tpl = (x: AgentTemplate): AgentTemplate => ({ ...x, mcpServers: arr(x.mcpServers), skills: arr(x.skills) });
const installed = (x: SkillsInstalled | null | undefined): SkillsInstalled => ({
  assigned: arr(x?.assigned),
  shared: arr(x?.shared),
  pending: arr(x?.pending),
  failed: arr(x?.failed),
});

export interface LibraryClient {
  agentTemplateList(): Promise<AgentTemplate[]>;
  agentTemplateSave(t: AgentTemplateInput): Promise<AgentTemplate>;
  agentTemplateDelete(id: string): Promise<void>;
  agentTemplateFromAgent(agentId: string): Promise<AgentTemplate>;
  agentTemplateApply(templateId: string, projectId: string, name?: string): Promise<AppliedTemplate>;
  /** The registry skills a template can carry. */
  agentTemplateSkillChoices(): Promise<SkillChoice[]>;
  claudePractices(role: string): Promise<ClaudePractices>;
  agentEnvGet(): Promise<AgentEnv>;
  /** Names only. A value is refused. */
  agentEnvSet(names: string[]): Promise<AgentEnv>;
}

export function createLibraryClient(t: Transport): LibraryClient {
  return {
    agentTemplateList: async () => arr(await t.call<AgentTemplate[] | null>('agenttemplate.list')).map(tpl),
    agentTemplateSave: async (x) => tpl(await t.call<AgentTemplate>('agenttemplate.save', x)),
    agentTemplateDelete: async (id) => {
      await t.call('agenttemplate.delete', { id });
    },
    agentTemplateFromAgent: async (agentId) => tpl(await t.call<AgentTemplate>('agenttemplate.fromAgent', { agentId })),
    agentTemplateApply: async (templateId, projectId, name) => {
      const r = await t.call<AppliedTemplate>('agenttemplate.apply', { templateId, projectId, name });
      return { ...r, granted: arr(r.granted), skipped: arr(r.skipped), skills: installed(r.skills) };
    },
    agentTemplateSkillChoices: async () => arr(await t.call<SkillChoice[] | null>('agenttemplate.skillChoices')).map((c) => ({ ...c, from: arr(c.from) })),
    claudePractices: (role) => t.call('claude.practices', { role }),
    agentEnvGet: async () => {
      const r = await t.call<AgentEnv>('agentenv.get');
      return { pass: arr(r.pass), kept: arr(r.kept), prefixes: arr(r.prefixes) };
    },
    agentEnvSet: async (names) => {
      const r = await t.call<AgentEnv>('agentenv.set', { names });
      return { pass: arr(r.pass), kept: arr(r.kept), prefixes: arr(r.prefixes) };
    },
  };
}
