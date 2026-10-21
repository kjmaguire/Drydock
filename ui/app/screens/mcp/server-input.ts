// Pure helpers of the MCP server form: the text fields are parsed here so Windows paths and quoted values need no shell
// quoting rules, and a save that changes one thing keeps everything else (stored secrets included) as it is.
//
// The names of environment variables are checked here, before anything is sent (audit 2026-10-16, L38): a name field may hold a
// pasted NAME=value, and a line that is not NAME=value may be a bare secret. The refusals name the line or the row and never hold
// any of what was typed.

import type { McpLibraryServer, McpServerInput } from '../../api/mcpClient.ts';
import { boxLines, ENV_NAME_RULE, isEnvName } from '../../lib/envname.ts';

/** One argument per line, so Windows paths and quoted values need no shell quoting rules. */
export const parseLines = (s: string): string[] =>
  s
    .split('\n')
    .map((l) => l.replace(/\r$/, ''))
    .filter((l) => l.trim() !== '');

/**
 * NAME=value per line, blank lines skipped. The first line that is not a valid name, an equals sign and a value is refused, and
 * counted as the person sees the lines in the box.
 */
export function parseEnv(s: string): { ok: true; env: Record<string, string> } | { ok: false; line: number } {
  const env: Record<string, string> = {};
  for (const { n, text } of boxLines(s)) {
    if (text.trim() === '') continue;
    const i = text.indexOf('=');
    const name = i > 0 ? text.slice(0, i).trim() : '';
    if (!isEnvName(name)) return { ok: false, line: n };
    env[name] = text.slice(i + 1);
  }
  return { ok: true, env };
}

/** What the environment box says about line `n`. It names the line and never repeats it. */
export const envLineError = (n: number): string => `Line ${n} is not NAME=value. ${ENV_NAME_RULE}`;
/** What a secret's name field says about row `n` (as numbered in its label). The value goes in its own field. */
export const secretNameError = (n: number): string => `Secret name ${n} is not a variable name. ${ENV_NAME_RULE} The value goes in its own field.`;

/** What is wrong with the environment fields of a program: the box, and each secret row by its place (undefined where it is fine). */
export interface FormProblems {
  env?: string;
  secrets: (string | undefined)[];
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

/**
 * The save input for the form's values, or what is wrong with the names of a program's environment variables (nothing is built
 * then, and nothing is sent). A web address has no environment, so its fields are not looked at.
 */
export function buildInput(v: FormValues, initial?: McpLibraryServer): { ok: true; input: McpServerInput } | { ok: false; problems: FormProblems } {
  const base = {
    id: v.id.trim(),
    display: v.display.trim(),
    transport: v.transport,
    guard: v.guard,
    readOnlyTools: initial?.readOnlyTools ?? [],
  };
  if (v.transport === 'http') {
    return {
      ok: true,
      input: {
        ...base,
        url: v.url.trim(),
        bearerToken: v.auth === 'token' ? v.token.trim() || undefined : undefined,
        clearBearer: (v.auth === 'token' ? v.clearToken : !!initial?.hasToken) || undefined,
        oauth: v.auth === 'oauth',
      },
    };
  }
  const env = parseEnv(v.env);
  // a name the server already holds is shown in a disabled field and cannot have been typed; a row with no name is dropped
  const secrets = v.secrets.map((row, i) => (!row.existing && row.name.trim() !== '' && !isEnvName(row.name.trim()) ? secretNameError(i + 1) : undefined));
  if (!env.ok || secrets.some((e) => e !== undefined)) return { ok: false, problems: { ...(env.ok ? {} : { env: envLineError(env.line) }), secrets } };
  return {
    ok: true,
    input: {
      ...base,
      argv: parseLines(v.argv),
      cwd: v.cwd.trim() || undefined,
      env: env.env,
      secretEnv: Object.fromEntries(v.secrets.filter((s) => s.name.trim()).map((s) => [s.name.trim(), s.value])),
    },
  };
}
