import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { NeedsYouItem, ProbeResult } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ProblemFix, problemOpenLabel } from './problem-fix.tsx';

afterEach(cleanup);

// TEST DOUBLES of the core's Inbox items for what went wrong in the background (lane B batch B6, F14 and H7)
const item = (over: Partial<NeedsYouItem>): NeedsYouItem => ({ key: 'k', kind: 'provider', severity: 1, title: 't', createdAt: new Date().toISOString(), ...over }) as NeedsYouItem;

describe('ProblemFix', () => {
  it('checks a provider that is down again in place and says what it answered', async () => {
    const asked: string[] = [];
    const s = testServices({
      providerTest: async (id) => {
        asked.push(id);
        return { ProviderID: id, OK: false, Error: 'GET /models: HTTP 503' } as ProbeResult;
      },
    });
    const r = mount(<ProblemFix item={item({ key: 'provider:p1:down:d', kind: 'provider', subject: 'p1' })} />, s);
    await r.act(() => byText(r.container, 'button', 'Check again').click());
    await waitFor(() => expect(r.$('[data-testid="problem-fix-result"]')!.textContent).toBe('Still not answering: GET /models: HTTP 503'), r);
    expect(asked).toEqual(['p1']);
  });

  it('retries a failed scan tool install in place', async () => {
    const asked: string[] = [];
    const s = testServices({ securityToolInstall: async (tool) => void asked.push(tool) });
    const r = mount(<ProblemFix item={item({ key: 'tool-install:osv-scanner:5', kind: 'tool_install', subject: 'osv-scanner' })} />, s);
    await r.act(() => byText(r.container, 'button', 'Retry').click());
    await waitFor(() => expect(r.$('[data-testid="problem-fix-result"]')?.textContent ?? '').toContain('Installing again'), r);
    expect(asked).toEqual(['osv-scanner']);
  });

  it('shows the sign-in of an MCP server whose sign-in expired, and Sign in starts it', async () => {
    const begun: string[] = [];
    const server = { id: 's1', display: 'Linear MCP', transport: 'http', url: 'https://mcp.linear.app/mcp', oauth: true, oauthAt: 0 } as unknown as McpLibraryServer;
    const s = testServices({
      mcpLibList: async () => [server],
      mcpOAuthStatus: async (id: string) => ({ id, state: 'idle', signedIn: false }),
      mcpOAuthBegin: async (id: string) => {
        begun.push(id);
        return { id, state: 'waiting', signedIn: false };
      },
    } as never);
    const r = mount(<ProblemFix item={item({ key: 'mcp-sign-in:s1:5', kind: 'mcp_sign_in', subject: 's1' })} />, s);
    await waitFor(() => byText(r.container, 'button', /Sign in/), r);
    await r.act(() => byText(r.container, 'button', /Sign in/).click());
    await waitFor(() => expect(begun).toEqual(['s1']), r);
  });

  it('names the Open button of each kind and has nothing in place for the kinds fixed on their own page', () => {
    expect(['integration', 'provider', 'mcp_sign_in', 'adapter_drift', 'tool_install', 'crash', 'security_findings'].map(problemOpenLabel)).toEqual([
      'Reconnect',
      'Open in Models',
      'Open the server',
      'Open on the Agents page',
      'Open Security',
      'Open Support',
      'Open Security',
    ]);
    expect(problemOpenLabel('approval')).toBe('');
    const r = mount(<ProblemFix item={item({ key: 'crash:c', kind: 'crash', subject: 'crash-1.txt' })} />);
    expect(r.$('[data-testid="problem-fix"]')).toBeNull();
  });
});
