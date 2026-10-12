import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { parseMcpConfig, slugFor } from './import.ts';

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

  it('makes short unique ids', () => {
    const taken = new Set(['ctx7']);
    expect(slugFor('ctx7', taken)).toBe('ctx72');
    expect(slugFor('ctx7', taken)).toBe('ctx73');
    expect(slugFor('123 Weird!! Name', taken)).toBe('weirdnam');
    expect(slugFor('---', taken)).toBe('srv');
  });
});
