// Typed client for the agent templates and the agents' environment (internal/core/ext_agenttemplates.go, ext_agentenv.go).

import type { Agent } from './types';
import type { Transport } from './transport';

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
  createdAt: number;
  updatedAt: number;
}

export type AgentTemplateInput = Omit<AgentTemplate, 'id' | 'createdAt' | 'updatedAt'> & { id?: string };

export interface AppliedTemplate {
  agent: Agent;
  granted: string[];
  /** A listed server that could not be granted, with the reason. */
  skipped: string[];
}

export interface AgentEnv {
  /** Extra variable names the human chose to pass to agents. */
  pass: string[];
  /** Names every agent process keeps, and prefixes kept for Claude Code. */
  kept: string[];
  prefixes: string[];
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const tpl = (x: AgentTemplate): AgentTemplate => ({ ...x, mcpServers: arr(x.mcpServers) });

export interface LibraryClient {
  agentTemplateList(): Promise<AgentTemplate[]>;
  agentTemplateSave(t: AgentTemplateInput): Promise<AgentTemplate>;
  agentTemplateDelete(id: string): Promise<void>;
  agentTemplateFromAgent(agentId: string): Promise<AgentTemplate>;
  agentTemplateApply(templateId: string, projectId: string, name?: string): Promise<AppliedTemplate>;
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
      return { ...r, granted: arr(r.granted), skipped: arr(r.skipped) };
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
