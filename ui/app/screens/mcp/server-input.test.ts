import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { libServer } from './fixtures.ts';
import { buildInput, formValues, inputOf, parseEnv, parseLines, whereOf } from './server-input.ts';

describe('MCP server form helpers', () => {
  it('reads one argument per line and NAME=value lines, and says which line is wrong', () => {
    expect(parseLines('npx\r\n-y\n\n  @x/y@1.0.0 \n')).toEqual(['npx', '-y', '  @x/y@1.0.0 ']);
    expect(parseEnv('A=1\nB=two=three', 'Environment')).toEqual({ A: '1', B: 'two=three' });
    expect(() => parseEnv('NOEQUALS', 'Environment')).toThrow('Environment: "NOEQUALS" is not NAME=value');
    expect(() => parseEnv('=x', 'Environment')).toThrow('is not NAME=value');
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
    expect(buildInput(v, libServer())).toEqual({
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
    expect(buildInput(v, web)).toMatchObject({ url: 'https://x/mcp', bearerToken: undefined, clearBearer: undefined, oauth: false });
    v.token = ' abc ';
    expect(buildInput(v, web).bearerToken).toBe('abc');
    v.auth = 'oauth';
    expect(buildInput(v, web)).toMatchObject({ bearerToken: undefined, clearBearer: true, oauth: true });
    expect(formValues(libServer({ oauth: true })).auth).toBe('oauth');
  });

  it('where a server runs, in one line', () => {
    expect(whereOf(libServer())).toBe('C:\\tools\\srv.exe --flag');
    expect(whereOf({ transport: 'http', url: 'https://x/mcp' })).toBe('https://x/mcp');
  });
});
