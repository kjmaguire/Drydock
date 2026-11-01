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
  /** An OAuth client the human registered with the provider is set (decision P122); its id and secret stay in the vault. */
  oauthClient?: boolean;
  guard: boolean;
  readOnlyTools: string[];
  tools: McpToolInfo[];
  discoveredAt: number;
  /** Ids of the projects that have the server. */
  projects: string[];
  createdAt: number;
  updatedAt: number;
  /** The catalog entry it was connected from (absent for a server the human described). */
  catalogId?: string;
  /** What the server said about itself the last time Drydock reached it. Outside text: shown as data. */
  serverInfo?: McpServerIdentity;
  /** Who the credential belongs to, as the server reported it. */
  account?: string;
  /** Why the last connection failed (absent after a success). */
  lastError?: string;
  lastErrorAt?: number;
}

/** A server's own identity from its answer to initialize (serverInfo and instructions). The server's claim, not Drydock's. */
export interface McpServerIdentity {
  name: string;
  title?: string;
  version?: string;
  description?: string;
  websiteUrl?: string;
  instructions?: string;
  protocolVersion?: string;
}

export type McpGalleryStatus = 'not_connected' | 'connected' | 'needs_sign_in' | 'needs_token' | 'error';

/** One catalog server as the connector gallery shows it (internal/core/ext_mcpgallery.go). */
export interface McpGalleryCard {
  id: string;
  display: string;
  publisher: string;
  official: boolean;
  /** The logo (ui/brand-logo.tsx). */
  brand: string;
  group?: string;
  summary: string;
  description: string;
  docsUrl?: string;
  transport: 'http' | 'stdio';
  url?: string;
  /** What a template runs on this computer. */
  command?: string[];
  /** none, bearer (paste a token), oauth (sign in in the browser) or env (a template's secret variable, secretEnv). */
  auth: 'none' | 'bearer' | 'oauth' | 'env' | '';
  authOptional: boolean;
  secretEnv?: string;
  tokenHelp?: string;
  /** The server can also sign in through an OAuth app the person registers with the provider (no dynamic registration, decision P122). */
  oauthApp?: { registerUrl: string; help?: string };
  /** The connected server signs in with OAuth; oauthClient: through a client the person registered. */
  oauth?: boolean;
  oauthClient?: boolean;
  template: boolean;
  license: string;
  licenseNotes?: string;
  /** The Drydock integration of the same brand. */
  integration?: string;
  /** The skill registries (internal/skills/registry.yaml ids) this vendor publishes; the Integrations page offers them next to the card. */
  skillRegistries: string[];
  trust: string;
  readOnlyTools: string[];
  tools: McpToolInfo[];
  /** The tools came from the server itself (not the catalog's declaration). */
  toolsListed: boolean;
  status: McpGalleryStatus;
  detail?: string;
  libraryId?: string;
  account?: string;
  serverInfo?: McpServerIdentity;
  projects: string[];
}

export interface McpConnectResult {
  card: McpGalleryCard;
  /** Set when the human has to sign in in the browser. */
  oauth?: McpOAuthStatus;
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
  /** The arguments of the call, as JSON text with the short fields (where it goes) first. Outside text: shown inert. */
  args: string;
  /** The paths and addresses the call names, each whole. */
  targets?: string[];
  /** A long value in `args` is cut; approving still covers all of it. */
  shortened?: boolean;
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

/** One column of the per-role tool table (docs/mcp.md section 3, decision P115). */
export interface RoleColumn {
  key: string;
  label: string;
  help: string;
  /** The Workspace tools of the column (none for a kind of server). */
  tools?: string[];
}

export interface RolePolicyCell {
  allowed: boolean;
  /** The table's own value. */
  default: boolean;
  /** A person set this cell for the project. */
  changed: boolean;
}

export interface RolePolicyRow {
  role: string;
  label: string;
  cells: Record<string, RolePolicyCell>;
}

/** A project's CEL rule: a tool its expression is true for is taken from the project's runs. */
export interface PolicyRule {
  id: string;
  name: string;
  expr: string;
}

/** mcp.rolePolicy.get: the project's table, its rules, and the switches some columns depend on. */
export interface RolePolicyView {
  projectId: string;
  columns: RoleColumn[];
  rows: RolePolicyRow[];
  rules: PolicyRule[];
  /** The project lets agents act (task, room, handoff); without it the Act column gives nothing. */
  actTools: boolean;
  /** This build has the repo map's parser. */
  repoMap: boolean;
}

/** One tool (or a whole server, "<id>__*") a task can keep. */
export interface TaskToolChoice {
  name: string;
  label: string;
  group: 'workspace' | 'server';
}

/** task.mcpTools.get: what a task can narrow its agents' tools to, and what it narrowed to (null: it does not narrow). */
export interface TaskToolsView {
  taskId: string;
  choices: TaskToolChoice[];
  narrowed: string[] | null;
  agentId?: string;
  /** What the task's agent gets now, with the narrowing. */
  agentTools: string[];
}

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
  /** The connector gallery: every catalog server with whether it is connected, and as whom. */
  mcpGallery(): Promise<McpGalleryCard[]>;
  /** Connects a catalog server: the token (or a template's secret) goes to the credential store and is never returned. */
  mcpConnect(catalogId: string, token?: string): Promise<McpConnectResult>;
  /** Connects a catalog server to sign in through the OAuth app the person registered (decision P122). */
  mcpConnectWithApp(catalogId: string, clientId: string, clientSecret: string): Promise<McpConnectResult>;
  /** Sets (or, with an empty id, removes) the registered OAuth client of a library server; it signs the server out. */
  mcpOAuthClient(id: string, clientId: string, clientSecret: string): Promise<McpLibraryServer>;
  /** The project's per-role tool table (decision P115). */
  mcpRolePolicyGet(projectId: string): Promise<RolePolicyView>;
  /** Sets one cell; null puts it back to the table's default. */
  mcpRolePolicySet(projectId: string, role: string, column: string, allowed: boolean | null): Promise<RolePolicyView>;
  /** Adds a CEL rule that takes tools away; one that does not compile is refused with the reason. */
  mcpPolicyRuleAdd(projectId: string, name: string, expr: string): Promise<RolePolicyView>;
  mcpPolicyRuleRemove(projectId: string, id: string): Promise<RolePolicyView>;
  /** Whether the project lets agents act (task, room, handoff). */
  mcpActToolsGet(projectId: string): Promise<boolean>;
  mcpActToolsSet(projectId: string, enabled: boolean): Promise<boolean>;
  taskMcpToolsGet(taskId: string): Promise<TaskToolsView>;
  /** Narrows the task's agents' tools to these names (globs); null stops narrowing. */
  taskMcpToolsSet(taskId: string, tools: string[] | null): Promise<TaskToolsView>;
}

const policy = (v: RolePolicyView): RolePolicyView => ({
  ...v,
  columns: arr(v.columns).map((c) => ({ ...c, tools: arr(c.tools) })),
  rows: arr(v.rows).map((r) => ({ ...r, cells: r.cells ?? {} })),
  rules: arr(v.rules),
});
const taskTools = (v: TaskToolsView): TaskToolsView => ({ ...v, choices: arr(v.choices), narrowed: v.narrowed ?? null, agentTools: arr(v.agentTools) });

const card = (c: McpGalleryCard): McpGalleryCard => ({ ...c, readOnlyTools: arr(c.readOnlyTools), tools: arr(c.tools), projects: arr(c.projects), skillRegistries: arr(c.skillRegistries), command: c.command ? arr(c.command) : undefined });

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
    mcpOAuthClient: async (id, clientId, clientSecret) => lib(await t.call<McpLibraryServer>('mcplib.oauth.client', { id, clientId, clientSecret })),
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
    mcpGallery: async () => arr(await t.call<McpGalleryCard[] | null>('mcplib.gallery', {})).map(card),
    mcpConnect: async (catalogId, token) => {
      const r = await t.call<McpConnectResult>('mcplib.connect', token ? { catalogId, token } : { catalogId });
      return { ...r, card: card(r.card) };
    },
    mcpConnectWithApp: async (catalogId, clientId, clientSecret) => {
      const r = await t.call<McpConnectResult>('mcplib.connect', { catalogId, clientId, clientSecret });
      return { ...r, card: card(r.card) };
    },
    mcpRolePolicyGet: async (projectId) => policy(await t.call<RolePolicyView>('mcp.rolePolicy.get', { projectId })),
    mcpRolePolicySet: async (projectId, role, column, allowed) => policy(await t.call<RolePolicyView>('mcp.rolePolicy.set', { projectId, role, column, allowed })),
    mcpPolicyRuleAdd: async (projectId, name, expr) => policy(await t.call<RolePolicyView>('mcp.policyRule.add', { projectId, name, expr })),
    mcpPolicyRuleRemove: async (projectId, id) => policy(await t.call<RolePolicyView>('mcp.policyRule.remove', { projectId, id })),
    mcpActToolsGet: async (projectId) => (await t.call<{ enabled: boolean }>('mcp.actTools.get', { projectId })).enabled === true,
    mcpActToolsSet: async (projectId, enabled) => (await t.call<{ enabled: boolean }>('mcp.actTools.set', { projectId, enabled })).enabled === true,
    taskMcpToolsGet: async (taskId) => taskTools(await t.call<TaskToolsView>('task.mcpTools.get', { taskId })),
    taskMcpToolsSet: async (taskId, tools) => taskTools(await t.call<TaskToolsView>('task.mcpTools.set', { taskId, tools })),
  };
}
