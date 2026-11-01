// Typed client for the agent templates and the agents' environment (internal/core/ext_agenttemplates.go, ext_agentenv.go).

import type { Agent, SkillRef, SkippedItem } from './types.ts';
import type { Transport } from './transport.ts';

/** A team member defined once in the workspace. Applying it makes an ordinary agent in a project that records the template (`templateId`) but does not follow it; only the editor's "Everywhere" carries a change back (decision P137). */
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

/** agent.draft's answer (decision P138): a drafted agent, not saved. `source` says whether a model or the offline rules wrote it. */
export interface AgentDraft {
  name: string;
  role: string;
  adapter: string;
  model: string;
  effort: string;
  permissionMode: string;
  purpose: string;
  skills: string[];
  /** Library MCP server ids. */
  tools: string[];
  why: string;
  source: 'model' | 'rules';
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

export type AgentTemplateInput = Omit<AgentTemplate, 'id' | 'createdAt' | 'updatedAt'> & {
  id?: string;
  /**
   * The person's confirmation of a permission mode that lets agents made from the template act without asking (bypassPermissions,
   * auto, dontAsk). Read when the template is saved, not stored; sent only after they ticked the box, and only for such a mode.
   */
  confirmSkipsApprovals?: boolean;
};

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
  /** `confirmSkipsApprovals`: the person's confirmation when the template's permission mode lets the agent act without asking. */
  agentTemplateApply(templateId: string, projectId: string, name?: string, confirmSkipsApprovals?: boolean): Promise<AppliedTemplate>;
  /** The registry skills a template can carry. */
  agentTemplateSkillChoices(): Promise<SkillChoice[]>;
  claudePractices(role: string): Promise<ClaudePractices>;
  /** Drafts an agent from a description; nothing is saved (decision P138). */
  agentDraft(description: string, projectId?: string): Promise<AgentDraft>;
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
    agentTemplateApply: async (templateId, projectId, name, confirmSkipsApprovals) => {
      const r = await t.call<AppliedTemplate>('agenttemplate.apply', { templateId, projectId, name, ...(confirmSkipsApprovals ? { confirmSkipsApprovals: true } : {}) });
      return { ...r, granted: arr(r.granted), skipped: arr(r.skipped), skills: installed(r.skills) };
    },
    agentTemplateSkillChoices: async () => arr(await t.call<SkillChoice[] | null>('agenttemplate.skillChoices')).map((c) => ({ ...c, from: arr(c.from) })),
    claudePractices: (role) => t.call('claude.practices', { role }),
    agentDraft: async (description, projectId) => {
      const d = await t.call<AgentDraft>('agent.draft', { description, ...(projectId ? { projectId } : {}) });
      return { ...d, skills: arr(d.skills), tools: arr(d.tools) };
    },
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
