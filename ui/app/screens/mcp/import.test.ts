import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { commandText, importedNote, parseMcpConfig, runsProgram, slugFor, startingChoice } from './import.ts';

/** The arguments of a command line by the rules of CommandLineToArgvW (2n backslashes and a quote: n backslashes, a quote that opens or closes; 2n+1: n backslashes and a quote). */
function readBack(line: string): string[] {
  const out: string[] = [];
  const n = line.length;
  let i = 0;
  while (i < n) {
    while (i < n && line[i] === ' ') i++;
    if (i >= n) break;
    let arg = '';
    let inQuotes = false;
    while (i < n) {
      let slashes = 0;
      while (i < n && line[i] === '\\') {
        slashes++;
        i++;
      }
      if (i < n && line[i] === '"') {
        arg += '\\'.repeat(Math.floor(slashes / 2));
        if (slashes % 2 === 1) arg += '"';
        else inQuotes = !inQuotes;
        i++;
      } else {
        arg += '\\'.repeat(slashes);
        if (i >= n || (line[i] === ' ' && !inQuotes)) break;
        arg += line[i];
        i++;
      }
    }
    out.push(arg);
  }
  return out;
}

describe('importing mcp.json / claude_desktop_config.json', () => {
  it('turns Claude Desktop servers into library inputs with their secrets write-only', () => {
    const r = parseMcpConfig(
      JSON.stringify({
        mcpServers: {
          'GitHub Local': { command: 'github-mcp-server', args: ['stdio', '--read-only'], env: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } },
          linear: { url: 'https://mcp.linear.app/mcp', headers: { Authorization: 'Bearer lin_api_1', 'X-Team': 'eng' } },
          old: { url: 'https://x.example/sse', type: 'sse' },
          prompt: { command: 'npx', env: { KEY: '${input:key}' } },
          nothing: {},
        },
      }),
      ['github'],
    );
    expect(r.candidates.map((c) => c.input.id)).toEqual(['githublo', 'linear']);
    const gh = r.candidates[0]!.input;
    expect(gh).toEqual({ id: 'githublo', display: 'GitHub Local', transport: 'stdio', argv: ['github-mcp-server', 'stdio', '--read-only'], secretEnv: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } });
    expect(gh.env).toBeUndefined();
    const lin = r.candidates[1]!;
    expect(lin.input.bearerToken).toBe('lin_api_1');
    expect(lin.notes.join(' ')).toContain('X-Team is not sent');
    expect(r.skipped.map((s) => s.name)).toEqual(['old', 'prompt', 'nothing']);
    expect(r.skipped[1]!.reason).toContain('placeholders');
  });

  it('reads VS Code\'s "servers" and refuses what is not a config', () => {
    const r = parseMcpConfig('{"servers":{"docs":{"type":"http","url":"https://learn.microsoft.com/api/mcp"}}}');
    expect(r.candidates[0]!.input).toEqual({ id: 'docs', display: 'docs', transport: 'http', url: 'https://learn.microsoft.com/api/mcp' });
    expect(() => parseMcpConfig('not json')).toThrow('not valid JSON');
    expect(() => parseMcpConfig('{"other":{}}')).toThrow('No "mcpServers"');
  });

  it('ticks only the servers that reach an address; a program is never ticked for the person (M24)', () => {
    const r = parseMcpConfig(
      JSON.stringify({
        mcpServers: {
          local: { command: 'node', args: ['server.js'] },
          remote: { url: 'https://example.com/mcp' },
          other: { command: 'npx', args: ['-y', 'x'], env: { K: 'v' } },
          web: { type: 'http', url: 'https://web.example/mcp', headers: { Authorization: 'Bearer t' } },
        },
      }),
    );
    expect(r.candidates.map((c) => [c.name, runsProgram(c)])).toEqual([
      ['local', true],
      ['remote', false],
      ['other', true],
      ['web', false],
    ]);
    expect(startingChoice(r)).toEqual(['remote', 'web']);
    expect(startingChoice({ candidates: [], skipped: [] })).toEqual([]);
  });

  it('writes a command line whole, quoting only the arguments that need it', () => {
    expect(commandText(['github-mcp-server', 'stdio', '--read-only'])).toBe('github-mcp-server stdio --read-only');
    expect(commandText(['C:\\Program Files\\x.exe', '--name', 'a b', '', 'say "hi"'])).toBe('"C:\\Program Files\\x.exe" --name "a b" "" "say \\"hi\\""');
    expect(commandText([])).toBe('');
  });

  it('quotes the way Windows reads a command line back: backslashes are doubled only where a quote follows them', () => {
    // a path with a space keeps its backslashes, except the ones that end it (they would escape the closing quote)
    expect(commandText(['C:\\My Dir\\'])).toBe('"C:\\My Dir\\\\"');
    // a quote after backslashes: the backslashes are doubled and the quote is escaped
    expect(commandText(['a\\"b'])).toBe('"a\\\\\\"b"');
    expect(commandText(['x\\\\"y'])).toBe('"x\\\\\\\\\\"y"');
    // a backslash that no quote follows, in an argument that needs no quotes, is left as it is
    expect(commandText(['C:\\dir\\', 'x'])).toBe('C:\\dir\\ x');
    // read back by the rules of CommandLineToArgvW, every line gives the arguments it was made from
    const args = ['C:\\My Dir\\', 'a\\"b', 'x\\\\"y', 'say "hi"', 'plain', '', 'two  spaces', 'tail\\\\'];
    expect(readBack(commandText(args))).toEqual(args);
  });

  it('says after an import that nothing has been started, and where to start one', () => {
    expect(importedNote(1, 'press Find tools on its card')).toBe(
      '1 server imported. Nothing has been started: no program has run and no address has been contacted. To start one, press Find tools on its card; what it will run is shown beside the button.',
    );
    expect(importedNote(3, 'x')).toContain('3 servers imported.');
  });

  it('makes short unique ids', () => {
    const taken = new Set(['ctx7']);
    expect(slugFor('ctx7', taken)).toBe('ctx72');
    expect(slugFor('ctx7', taken)).toBe('ctx73');
    expect(slugFor('123 Weird!! Name', taken)).toBe('weirdnam');
    expect(slugFor('---', taken)).toBe('srv');
  });
});
