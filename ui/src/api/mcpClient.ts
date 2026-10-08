// Typed client for the user-added MCP servers (internal/core/ext_mcpservers.go).

import type { Transport } from './transport';

export interface McpToolInfo {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
  /** What the server CLAIMS. Drydock never trusts it; the human decides which tools are read-only. */
  readOnlyHint: boolean;
}

export interface McpAccess {
  agentId: string;
  tools: string[];
}

export interface McpServer {
  projectId: string;
  id: string;
  display: string;
  transport: 'stdio' | 'http';
  url?: string;
  argv?: string[];
  cwd?: string;
  env?: Record<string, string>;
  /** Names only: the values are in the credential store and are never sent to the UI. */
  secretEnv: string[];
  hasToken: boolean;
  guard: boolean;
  readOnlyTools: string[];
  tools: McpToolInfo[];
  discoveredAt: number;
  enabled: boolean;
  agents: McpAccess[];
  createdAt: number;
  updatedAt: number;
}

/** Secrets are write-only: an empty value keeps the stored one, and a secret name left out is removed. */
export interface McpServerInput {
  projectId: string;
  id: string;
  display: string;
  transport: 'stdio' | 'http';
  url?: string;
  argv?: string[];
  cwd?: string;
  env?: Record<string, string>;
  secretEnv?: Record<string, string>;
  bearerToken?: string;
  clearBearer?: boolean;
  guard?: boolean;
  readOnlyTools?: string[];
  enabled?: boolean;
}

export interface McpApproval {
  id: string;
  server: string;
  tool: string;
  agentId: string;
  taskId?: string;
  /** The exact arguments of the call, as JSON text. Outside text: shown inert. */
  args: string;
  created: number;
  expiresAt: number;
  projectId: string;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const server = (s: McpServer): McpServer => ({ ...s, secretEnv: arr(s.secretEnv), readOnlyTools: arr(s.readOnlyTools), tools: arr(s.tools), agents: arr(s.agents).map((a) => ({ ...a, tools: arr(a.tools) })) });

export interface McpClient {
  mcpServerList(projectId: string): Promise<McpServer[]>;
  mcpServerSave(input: McpServerInput): Promise<McpServer>;
  mcpServerDelete(projectId: string, id: string): Promise<void>;
  /** Connects to the server, lists its tools and stores them. Also the "test connection". */
  mcpServerDiscover(projectId: string, id: string): Promise<McpServer>;
  mcpServerAccessSet(projectId: string, serverId: string, agents: McpAccess[]): Promise<McpServer>;
  mcpApprovalList(projectId: string): Promise<McpApproval[]>;
  mcpApprovalDecide(approvalId: string, approve: boolean): Promise<void>;
}

export function createMcpClient(t: Transport): McpClient {
  return {
    mcpServerList: async (projectId) => arr(await t.call<McpServer[] | null>('mcpserver.list', { projectId })).map(server),
    mcpServerSave: async (input) => server(await t.call<McpServer>('mcpserver.save', input)),
    mcpServerDelete: async (projectId, id) => {
      await t.call('mcpserver.delete', { projectId, id });
    },
    mcpServerDiscover: async (projectId, id) => server(await t.call<McpServer>('mcpserver.discover', { projectId, id })),
    mcpServerAccessSet: async (projectId, serverId, agents) => server(await t.call<McpServer>('mcpserver.access.set', { projectId, serverId, agents })),
    mcpApprovalList: async (projectId) => arr(await t.call<McpApproval[] | null>('mcpserver.approval.list', { projectId })),
    mcpApprovalDecide: async (approvalId, approve) => {
      await t.call('mcpserver.approval.decide', { approvalId, approve });
    },
  };
}
