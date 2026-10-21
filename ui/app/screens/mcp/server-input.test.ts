import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { libServer } from './fixtures.ts';
import type { McpLibraryServer, McpServerInput } from '../../api/mcpClient.ts';
import { buildInput, envLineError, formValues, inputOf, parseEnv, parseLines, secretNameError, whereOf, type FormValues } from './server-input.ts';

/** The input a form's values build; a form that was refused fails the test with what it said. */
function inputFrom(v: FormValues, initial?: McpLibraryServer): McpServerInput {
  const b = buildInput(v, initial);
  if (!b.ok) throw new Error(`the form was refused: ${JSON.stringify(b.problems)}`);
  return b.input;
}

describe('MCP server form helpers', () => {
  it('reads one argument per line and NAME=value lines', () => {
    expect(parseLines('npx\r\n-y\n\n  @x/y@1.0.0 \n')).toEqual(['npx', '-y', '  @x/y@1.0.0 ']);
    expect(parseEnv('A=1\nB=two=three')).toEqual({ ok: true, env: { A: '1', B: 'two=three' } });
    // a name is trimmed, a value is kept as it was typed, blank lines and a carriage return are not lines
    expect(parseEnv('  A = 1 \r\n\r\n_b2=\n')).toEqual({ ok: true, env: { A: ' 1 ', _b2: '' } });
    expect(parseEnv('')).toEqual({ ok: true, env: {} });
  });

  it('refuses the first line that is not a valid name, an equals sign and a value, counted as the box shows its lines (L38)', () => {
    expect(parseEnv('NOEQUALS')).toEqual({ ok: false, line: 1 });
    expect(parseEnv('=x')).toEqual({ ok: false, line: 1 });
    expect(parseEnv('A=1\n\n  \nB C=2\nD=4')).toEqual({ ok: false, line: 4 });
    const notNames = ['1A=x', 'A-B=x', ' =x', 'A.B=x', 'x'.repeat(65) + '=1', 'ghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5'];
    expect(notNames.filter((l) => parseEnv(`OK=1\n${l}`).ok)).toEqual([]);
    expect(notNames.filter((l) => JSON.stringify(parseEnv(`OK=1\n${l}`)) !== '{"ok":false,"line":2}')).toEqual([]);
    // what the message says is the line's number and the rule, not a word of the line
    const said = envLineError(2);
    expect(said).toBe('Line 2 is not NAME=value. A name has letters, digits and _ only, at most 64, and does not start with a digit.');
    expect(secretNameError(3)).toBe('Secret name 3 is not a variable name. A name has letters, digits and _ only, at most 64, and does not start with a digit. The value goes in its own field.');
  });

  it('rebuilds a save input that keeps the stored secrets without sending them', () => {
    const i = inputOf(libServer());
    expect(i.secretEnv).toEqual({ API_KEY: '' });
    expect(i.bearerToken).toBeUndefined();
    expect(inputOf(libServer(), { readOnlyTools: ['echo'] }).readOnlyTools).toEqual(['echo']);
  });

  it('an edit starts from the server (never its secrets); a program sends its command, folder, environment and secrets', () => {
    const v = formValues(libServer());
    expect(v).toMatchObject({ id: 'tst', argv: 'C:\\tools\\srv.exe\n--flag', env: 'MODE=x', auth: 'none', guard: true });
    expect(v.secrets).toEqual([{ name: 'API_KEY', value: '', existing: true }]);
    v.secrets.push({ name: ' TOKEN ', value: 's', existing: false }, { name: ' ', value: 'dropped', existing: false });
    expect(inputFrom(v, libServer())).toEqual({
      id: 'tst',
      display: 'Test server',
      transport: 'stdio',
      guard: true,
      readOnlyTools: [],
      argv: ['C:\\tools\\srv.exe', '--flag'],
      cwd: undefined,
      env: { MODE: 'x' },
      secretEnv: { API_KEY: '', TOKEN: 's' },
    });
  });

  it('a web server sends a token only when one is typed, and forgets a stored token when the sign-in changes', () => {
    const web = libServer({ transport: 'http', url: 'https://x/mcp', argv: undefined, hasToken: true, secretEnv: [] });
    const v = formValues(web);
    expect(v.auth).toBe('token');
    expect(inputFrom(v, web)).toMatchObject({ url: 'https://x/mcp', bearerToken: undefined, clearBearer: undefined, oauth: false });
    v.token = ' abc ';
    expect(inputFrom(v, web).bearerToken).toBe('abc');
    v.auth = 'oauth';
    expect(inputFrom(v, web)).toMatchObject({ bearerToken: undefined, clearBearer: true, oauth: true });
    expect(formValues(libServer({ oauth: true })).auth).toBe('oauth');
  });

  it('builds nothing from an environment that has a line or a secret name that is not a name, and says which, never what (L38)', () => {
    const v = formValues(libServer());
    // a pasted NAME=value in the box is fine as a line, but a bare value or a name that is not one is refused
    v.env = 'MODE=x\n\nghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5';
    expect(buildInput(v, libServer())).toEqual({ ok: false, problems: { env: envLineError(3), secrets: [undefined] } });
    // a pasted NAME=value in a secret name field: the existing row (a disabled field) is not looked at, the typed ones are
    v.env = 'MODE=x';
    v.secrets.push({ name: ' API_KEY=abc123 ', value: 's', existing: false }, { name: 'GOOD_ONE', value: 's', existing: false }, { name: '1BAD', value: 's', existing: false }, { name: '  ', value: 's', existing: false });
    const refused = buildInput(v, libServer());
    expect(refused).toEqual({ ok: false, problems: { secrets: [undefined, secretNameError(2), undefined, secretNameError(4), undefined] } });
    const said = JSON.stringify(refused);
    expect(['abc123', 'API_KEY=', '1BAD'].filter((piece) => said.includes(piece))).toEqual([]);
    // both at once: the box and the rows are all reported
    v.env = 'nonsense';
    const both = buildInput(v, libServer());
    expect(both.ok ? undefined : both.problems.env).toBe(envLineError(1));
    expect(both.ok ? undefined : both.problems.secrets.filter(Boolean).length).toBe(2);
    // a corrected form builds
    v.env = 'MODE=x';
    v.secrets = v.secrets.filter((row) => row.name.trim() === 'GOOD_ONE' || row.existing);
    expect(inputFrom(v, libServer()).secretEnv).toEqual({ API_KEY: '', GOOD_ONE: 's' });
  });

  it('a web server has no environment: what is left in those fields is not looked at', () => {
    const web = libServer({ transport: 'http', url: 'https://x/mcp', argv: undefined, hasToken: false, secretEnv: [] });
    const v = formValues(web);
    v.env = 'not a line';
    v.secrets = [{ name: 'A=b', value: 'x', existing: false }];
    const input = inputFrom(v, web);
    expect(input).toMatchObject({ url: 'https://x/mcp', oauth: false });
    expect('env' in input).toBe(false);
    expect('secretEnv' in input).toBe(false);
  });

  it('where a server runs, in one line', () => {
    expect(whereOf(libServer())).toBe('C:\\tools\\srv.exe --flag');
    expect(whereOf({ transport: 'http', url: 'https://x/mcp' })).toBe('https://x/mcp');
  });
});
