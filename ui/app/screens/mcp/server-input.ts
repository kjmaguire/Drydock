// Pure helpers of the MCP server form: the text fields are parsed here so Windows paths and quoted values need no shell
// quoting rules, and a save that changes one thing keeps everything else (stored secrets included) as it is.

import type { McpLibraryServer, McpServerInput } from '../../api/mcpClient.ts';

/** One argument per line, so Windows paths and quoted values need no shell quoting rules. */
export const parseLines = (s: string): string[] =>
  s
    .split('\n')
    .map((l) => l.replace(/\r$/, ''))
    .filter((l) => l.trim() !== '');

/** NAME=value per line. Throws a message the form shows. */
export function parseEnv(s: string, what: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of parseLines(s)) {
    const i = line.indexOf('=');
    if (i <= 0) throw new Error(`${what}: "${line.trim()}" is not NAME=value`);
    out[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return out;
}

/** The save input that keeps everything about a server as it is (secrets stay in the store, empty values keep them). */
export function inputOf(s: McpLibraryServer, over: Partial<McpServerInput> = {}): McpServerInput {
  return {
    id: s.id,
    display: s.display,
    transport: s.transport,
    url: s.url,
    argv: s.argv,
    cwd: s.cwd,
    env: s.env,
    secretEnv: Object.fromEntries(s.secretEnv.map((n) => [n, ''])),
    guard: s.guard,
    readOnlyTools: s.readOnlyTools,
    ...over,
  };
}

/** Where a server runs, as one line: the address of a web server, the command line of a program. */
export const whereOf = (s: Pick<McpLibraryServer, 'transport' | 'url' | 'argv'>): string => (s.transport === 'http' ? (s.url ?? '') : (s.argv ?? []).join(' '));

export type AuthChoice = 'none' | 'token' | 'oauth';

export interface SecretRow {
  name: string;
  value: string;
  existing: boolean;
}

export interface FormValues {
  id: string;
  display: string;
  transport: 'stdio' | 'http';
  url: string;
  argv: string;
  cwd: string;
  env: string;
  secrets: SecretRow[];
  token: string;
  clearToken: boolean;
  auth: AuthChoice;
  guard: boolean;
}

/** The form's starting values: empty for a new server, the server's own values (never its secrets) for an edit. */
export function formValues(initial?: McpLibraryServer): FormValues {
  return {
    id: initial?.id ?? '',
    display: initial?.display ?? '',
    transport: initial?.transport ?? 'stdio',
    url: initial?.url ?? '',
    argv: (initial?.argv ?? []).join('\n'),
    cwd: initial?.cwd ?? '',
    env: Object.entries(initial?.env ?? {})
      .map(([k, v]) => `${k}=${v}`)
      .join('\n'),
    secrets: (initial?.secretEnv ?? []).map((n) => ({ name: n, value: '', existing: true })),
    token: '',
    clearToken: false,
    auth: initial?.oauth ? 'oauth' : initial?.hasToken ? 'token' : 'none',
    guard: initial?.guard ?? true,
  };
}

/** The save input for the form's values. Throws (with the line) when the environment is not NAME=value lines. */
export function buildInput(v: FormValues, initial?: McpLibraryServer): McpServerInput {
  const env = parseEnv(v.env, 'Environment');
  return {
    id: v.id.trim(),
    display: v.display.trim(),
    transport: v.transport,
    guard: v.guard,
    readOnlyTools: initial?.readOnlyTools ?? [],
    ...(v.transport === 'http'
      ? {
          url: v.url.trim(),
          bearerToken: v.auth === 'token' ? v.token.trim() || undefined : undefined,
          clearBearer: (v.auth === 'token' ? v.clearToken : !!initial?.hasToken) || undefined,
          oauth: v.auth === 'oauth',
        }
      : {
          argv: parseLines(v.argv),
          cwd: v.cwd.trim() || undefined,
          env,
          secretEnv: Object.fromEntries(v.secrets.filter((s) => s.name.trim()).map((s) => [s.name.trim(), s.value])),
        }),
  };
}
