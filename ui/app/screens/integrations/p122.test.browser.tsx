import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { IntegrationAttachment, WebhookStatus } from '../../api/extTypes.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';
import { BackgroundPanel } from './background-panel.tsx';
import { item } from './fixtures.ts';
import { ItemAttachments } from './project-extras.tsx';

afterEach(cleanup);

// TEST DOUBLES only: partial clients answering with the shapes internal/integrations returns for decision P122 (GitHub and
// Aikido on the webhook receiver, where an attachment was found).

const need = (r: ReturnType<typeof mount>, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);

const status: WebhookStatus = {
  enabled: true,
  port: 8787,
  listening: true,
  address: 'http://127.0.0.1:8787',
  endpoints: [
    {
      kind: 'github',
      label: 'GitHub',
      path: '/hooks/github',
      url: 'http://127.0.0.1:8787/hooks/github',
      integrationId: 'int_gh',
      hasSecret: true,
      secret: 'webhook secret',
      header: 'X-Hub-Signature-256',
      where: 'GitHub: the repository settings, Webhooks.',
      commands: ['gh webhook forward --repo=acme/web --events=issues,issue_comment --url=http://127.0.0.1:8787/hooks/github --secret=<the same secret>'],
    },
    { kind: 'aikido', label: 'Aikido', path: '/hooks/aikido', url: 'http://127.0.0.1:8787/hooks/aikido', integrationId: 'int_aik', hasSecret: false, secret: 'signing secret', header: 'X-Aikido-Webhook-Signature', where: 'Aikido: Integrations, Webhooks.' },
  ],
  queued: 0,
  recent: [],
  exposeNote: 'Drydock listens only on this computer (127.0.0.1).',
};

describe('GitHub and Aikido webhooks (decision P122)', () => {
  it("shows GitHub's forwarder command for the assigned repository, and Aikido's address and secret", async () => {
    const s = testServices({ integrationWebhooks: async () => status, integrationSweep: async () => ({ enabled: true, everyMs: 1 }) } as Partial<Client>);
    const r = mount(<BackgroundPanel />, s);
    const cmd = await need(r, '[data-testid="webhook-command"]');
    expect(cmd.textContent).toContain('gh webhook forward --repo=acme/web');
    expect(cmd.textContent).toContain('--url=http://127.0.0.1:8787/hooks/github');
    const eps = Array.from(r.$$('[data-testid="webhook-endpoint"]')) as HTMLElement[];
    expect(eps.length).toBe(2);
    expect(eps[1]!.textContent).toContain('http://127.0.0.1:8787/hooks/aikido');
    expect(eps[1]!.textContent).toContain('no signing secret yet: deliveries are refused');
    // the page is no wider than its column with a long command in it
    expect(r.container.scrollWidth <= r.container.clientWidth + 1).toBe(true);
  });
});

describe('where an attachment was found (decision P122)', () => {
  it('says whether a file is in the page body, the issue text or a files property', async () => {
    const base = { itemId: 'itm_1', integrationId: 'int_1', mime: 'image/png', size: 10, textKept: false, redactions: 0, risk: 0, listedAt: 1, untrusted: true };
    const atts: IntegrationAttachment[] = [
      { ...base, id: 'att_1', externalId: 'block:b1', name: 'diagram.png', downloadable: true, source: 'page body' },
      { ...base, id: 'att_2', externalId: 'body:x', name: 'crash.log', downloadable: true, source: 'issue body' },
      { ...base, id: 'att_3', externalId: 'Files#0', name: 'spec.pdf', downloadable: true, source: 'property' },
    ];
    const s = testServices({ integrationAttachments: async () => atts } as Partial<Client>);
    const r = mount(<ItemAttachments item={item()} />, s);
    await waitFor(() => expect(r.$$('[data-testid="attachment-source"]').length).toBe(3), r);
    const words = Array.from(r.$$('[data-testid="attachment-source"]')).map((e) => e.textContent);
    expect(words).toEqual([' · in the page body', ' · in the issue text', ' · in a files property']);
    expect(byText(r.container, 'button', 'Download')).not.toBeNull();
  });
});

describe("a library server's own OAuth app (decision P122)", () => {
  it('after a sign-in the provider refused to register for, takes the client id and secret and sends them once', async () => {
    const calls: unknown[][] = [];
    const s = testServices({
      mcpOAuthStatus: async () => ({ id: 'ghlike', state: 'failed', signedIn: false, error: 'this provider does not let Drydock register itself: register an OAuth app' }),
      mcpOAuthClient: async (...a: unknown[]) => {
        calls.push(a);
        return { id: 'ghlike', oauthClient: true } as McpLibraryServer;
      },
    } as Partial<Client>);
    const r = mount(<OAuthPanel server={{ id: 'ghlike', oauthAt: 0 } as McpLibraryServer} onChanged={() => {}} />, s);
    await waitFor(() => byText(r.container, 'button', 'Use your own OAuth app'), r);
    await r.act(() => byText(r.container, 'button', 'Use your own OAuth app').click());
    const form = await need(r, '[data-testid="oauth-app-form"]');
    const secret = byLabel<HTMLInputElement>(form, 'Client secret (if the app has one)');
    expect(secret.type).toBe('password');
    type(byLabel<HTMLInputElement>(form, 'Client id of your OAuth app'), 'Iv1.app');
    type(secret, 'app-secret-zz9');
    await r.act(() => byText(form, 'button', 'Save the app').click());
    await waitFor(() => expect(calls).toEqual([['ghlike', 'Iv1.app', 'app-secret-zz9']]), r);
    expect(r.container.textContent).not.toContain('app-secret-zz9');
  });

  it('says a server signs in through the app and removes it on request', async () => {
    const calls: unknown[][] = [];
    const s = testServices({
      mcpOAuthStatus: async () => ({ id: 'ghlike', state: 'idle', signedIn: false }),
      mcpOAuthClient: async (...a: unknown[]) => {
        calls.push(a);
        return { id: 'ghlike', oauthClient: false } as McpLibraryServer;
      },
    } as Partial<Client>);
    const r = mount(<OAuthPanel server={{ id: 'ghlike', oauthAt: 0, oauthClient: true } as McpLibraryServer} onChanged={() => {}} />, s);
    const note = await need(r, '[data-testid="oauth-app"]');
    expect(note.textContent).toContain('Signs in through your own OAuth app.');
    expect(r.$('[data-testid="oauth-app-form"]')).toBeNull();
    await r.act(() => byText(note, 'button', 'Remove the app').click());
    await waitFor(() => expect(calls).toEqual([['ghlike', '', '']]), r);
  });
});
