// Plain data fixtures for the MCP and Library tests (no behavior). Override any field with the `over` argument.

import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { McpLibraryServer, McpServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, Agent, Project } from '../../api/types.ts';

export const agent = (id: string, name: string, role: string): Agent =>
  ({ id, projectId: 'prj_1', name, adapter: 'claude', role, model: '', effort: '', providerId: '', permissionMode: 'acceptEdits', status: 'STOPPED' }) as Agent;
export const pay = agent('agt_1', 'Pay', 'backend');
export const rev = agent('agt_2', 'Rev', 'reviewer');

export const libServer = (over: Partial<McpLibraryServer> = {}): McpLibraryServer => ({
  id: 'tst',
  display: 'Test server',
  transport: 'stdio',
  argv: ['C:\\tools\\srv.exe', '--flag'],
  env: { MODE: 'x' },
  secretEnv: ['API_KEY'],
  hasToken: false,
  guard: true,
  readOnlyTools: [],
  tools: [
    { name: 'echo', description: 'echo text', readOnlyHint: true },
    { name: 'post_note', readOnlyHint: false },
  ],
  discoveredAt: 1760000000000,
  projects: ['prj_1'],
  createdAt: 1,
  updatedAt: 1,
  ...over,
});

export const projServer = (over: Partial<McpServer> = {}): McpServer => ({ ...libServer(), projectId: 'prj_1', enabled: true, agents: [], ...over });

export const projects = [
  { id: 'prj_1', name: 'Alpha' },
  { id: 'prj_2', name: 'Beta' },
] as Project[];

export const tpl = (over: Partial<AgentTemplate> = {}): AgentTemplate => ({
  id: 'tpl_1',
  name: 'Payments dev',
  role: 'backend',
  purpose: 'Own the payments service.',
  adapter: 'claude',
  model: 'sonnet',
  effort: 'high',
  permissionMode: '',
  providerId: '',
  mcpServers: ['tst'],
  skills: [],
  createdAt: 1,
  updatedAt: 1,
  ...over,
});

export const claude = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready' },
  capabilities: { modelSelection: true, effortControl: true, efforts: ['low', 'high'], models: ['sonnet'], permissionModes: ['acceptEdits', 'plan'] },
} as unknown as AdapterInfo;

export const devLicense = { configured: false, devMode: true, state: 'none', readOnly: false, message: '' } as never;
