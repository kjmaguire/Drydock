// Typed client for the user-added MCP servers (internal/core/ext_mcpservers.go).

import type { Transport } from './transport.ts';

export interface McpToolInfo {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
  /** What the server CLAIMS. Drydock never trusts it; the human decides which tools are read-only. */
  readOnlyHint: boolean;
  /**
   * Instruction-like patterns Drydock found in the tool's name, description or input schema when it listed the tools (tool
   * poisoning). A flagged tool is left out of the grants Drydock makes.
   */
  flags?: string[];
}

export interface McpAccess {
  agentId: string;
  tools: string[];
}

/** A server connected in the workspace library. Secrets are never here: only whether they exist, and the variable names. */
export interface McpLibraryServer {
  id: string;
  display: string;
  transport: 'stdio' | 'http';
  url?: string;
  argv?: string[];
  cwd?: string;
  env?: Record<string, string>;
  secretEnv: string[];
  hasToken: boolean;
  /** The server uses OAuth: the human signs in once and Drydock keeps and refreshes the tokens. */
  oauth?: boolean;
  /** When the human last signed in (0 or absent: not signed in). */
  oauthAt?: number;
  oauthScope?: string;
  guard: boolean;
  readOnlyTools: string[];
  tools: McpToolInfo[];
  discoveredAt: number;
  /** Ids of the projects that have the server. */
  projects: string[];
  createdAt: number;
  updatedAt: number;
}

/** A library server as one project has it. */
export interface McpServer extends McpLibraryServer {
  projectId: string;
  enabled: boolean;
  agents: McpAccess[];
}

/** Secrets are write-only: an empty value keeps the stored one, and a secret name left out is removed. */
export interface McpServerInput {
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
  /** true: sign in with OAuth; false: turn it off and forget the tokens; absent: keep as it is. */
  oauth?: boolean;
  guard?: boolean;
  readOnlyTools?: string[];
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

/** One agent CLI as the gateway sees it (mcp.status). */
export interface McpAdapterRow {
  adapter: string;
  /** The gateway is attached to this CLI's sessions (proven, or allowed by the human). */
  attached: boolean;
  reason?: string;
  /** The mechanism is not proven by a real run yet, but the human may allow it. */
  canTry: boolean;
  /** The human allowed it. */
  trying: boolean;
}

export interface McpStatus {
  running: boolean;
  address: string;
  adapters: McpAdapterRow[];
}

/** The state of one server's OAuth sign-in. */
export interface McpOAuthStatus {
  id: string;
  state: 'idle' | 'waiting' | 'failed';
  /** The sign-in page to open in a browser, while waiting. It comes from the server, so it is checked before it is linked. */
  url?: string;
  error?: string;
  signedIn: boolean;
  at?: number;
  scope?: string;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const lib = <T extends McpLibraryServer>(s: T): T => ({ ...s, secretEnv: arr(s.secretEnv), readOnlyTools: arr(s.readOnlyTools), tools: arr(s.tools), projects: arr(s.projects) });
const server = (s: McpServer): McpServer => ({ ...lib(s), agents: arr(s.agents).map((a) => ({ ...a, tools: arr(a.tools) })) });

export interface McpClient {
  /** The library: servers connected once for the whole workspace. */
  mcpLibList(): Promise<McpLibraryServer[]>;
  mcpLibSave(input: McpServerInput): Promise<McpLibraryServer>;
  /** Disconnects the server everywhere: every project loses it and its stored secrets are deleted. */
  mcpLibDelete(id: string): Promise<void>;
  /** Connects to the server, lists its tools and stores them. Also the "test connection". */
  mcpLibDiscover(id: string): Promise<McpLibraryServer>;
  /** The servers one project has. */
  mcpServerList(projectId: string): Promise<McpServer[]>;
  mcpServerAdd(projectId: string, serverId: string): Promise<McpServer>;
  mcpServerRemove(projectId: string, serverId: string): Promise<void>;
  mcpServerSetEnabled(projectId: string, serverId: string, enabled: boolean): Promise<McpServer>;
  mcpServerAccessSet(projectId: string, serverId: string, agents: McpAccess[]): Promise<McpServer>;
  /** Grants or revokes one agent. Granting also puts the server into the agent's project. */
  mcpServerGrant(projectId: string, serverId: string, agentId: string, on: boolean): Promise<McpServer>;
  /** OAuth sign-in of an http server: begin returns the sign-in page; poll the status (the core also publishes events). */
  mcpOAuthBegin(id: string): Promise<McpOAuthStatus>;
  mcpOAuthStatus(id: string): Promise<McpOAuthStatus>;
  mcpOAuthCancel(id: string): Promise<McpOAuthStatus>;
  /** Forgets the tokens; the server stays set up for OAuth. */
  mcpOAuthSignOut(id: string): Promise<McpOAuthStatus>;
  /** Whether the gateway runs and which agent CLIs it can be given to. */
  mcpStatus(): Promise<McpStatus>;
  /** Allows or stops the gateway for a CLI whose per-run MCP mechanism no real run has proven yet. */
  mcpTrySet(adapter: string, enabled: boolean): Promise<McpStatus>;
  mcpApprovalList(projectId: string): Promise<McpApproval[]>;
  mcpApprovalDecide(approvalId: string, approve: boolean): Promise<void>;
}

export function createMcpClient(t: Transport): McpClient {
  return {
    mcpLibList: async () => arr(await t.call<McpLibraryServer[] | null>('mcplib.list')).map(lib),
    mcpLibSave: async (input) => lib(await t.call<McpLibraryServer>('mcplib.save', input)),
    mcpLibDelete: async (id) => {
      await t.call('mcplib.delete', { id });
    },
    mcpLibDiscover: async (id) => lib(await t.call<McpLibraryServer>('mcplib.discover', { id })),
    mcpServerList: async (projectId) => arr(await t.call<McpServer[] | null>('mcpserver.list', { projectId })).map(server),
    mcpServerAdd: async (projectId, serverId) => server(await t.call<McpServer>('mcpserver.add', { projectId, serverId })),
    mcpServerRemove: async (projectId, serverId) => {
      await t.call('mcpserver.remove', { projectId, serverId });
    },
    mcpServerSetEnabled: async (projectId, serverId, enabled) => server(await t.call<McpServer>('mcpserver.setEnabled', { projectId, serverId, enabled })),
    mcpServerAccessSet: async (projectId, serverId, agents) => server(await t.call<McpServer>('mcpserver.access.set', { projectId, serverId, agents })),
    mcpServerGrant: async (projectId, serverId, agentId, on) => server(await t.call<McpServer>('mcpserver.grant', { projectId, serverId, agentId, on })),
    mcpOAuthBegin: (id) => t.call('mcplib.oauth.begin', { id }),
    mcpOAuthStatus: (id) => t.call('mcplib.oauth.status', { id }),
    mcpOAuthCancel: (id) => t.call('mcplib.oauth.cancel', { id }),
    mcpOAuthSignOut: (id) => t.call('mcplib.oauth.signout', { id }),
    mcpStatus: async () => {
      const s = await t.call<McpStatus>('mcp.status');
      return { ...s, adapters: arr(s.adapters) };
    },
    mcpTrySet: async (adapter, enabled) => {
      const s = await t.call<McpStatus>('mcp.try.set', { adapter, enabled });
      return { ...s, adapters: arr(s.adapters) };
    },
    mcpApprovalList: async (projectId) => arr(await t.call<McpApproval[] | null>('mcpserver.approval.list', { projectId })),
    mcpApprovalDecide: async (approvalId, approve) => {
      await t.call('mcpserver.approval.decide', { approvalId, approve });
    },
  };
}
