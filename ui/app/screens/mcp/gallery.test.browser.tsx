import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { devLicense, galleryCard, libServer, pay, projServer, rev } from './fixtures.ts';
import { galleryGroups, identityLine } from './gallery.tsx';
import { McpScreen } from './mcp-screen.tsx';

afterEach(cleanup);

/** The element, or a throw so waitFor keeps waiting. */
const must = <T,>(x: T | null | undefined): T => {
  if (x == null) throw new Error('not there yet');
  return x;
};

// TEST DOUBLES (labelled): the client below answers like the core's mcplib.gallery and mcplib.connect.
function show(client: Partial<Client>, opts: { projects?: { id: string; name: string }[] } = {}) {
  const s = testServices({
    licenseStatus: async () => devLicense,
    appInfo: async () => ({}) as never,
    projectList: async () => (opts.projects ?? [{ id: 'prj_1', name: 'gocalc' }]) as never,
    agentList: async () => [pay, rev],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    presenceList: async () => [],
    taskList: async () => [],
    mcpApprovalList: async () => [],
    mcpServerList: async () => [],
    mcpLibList: async () => [],
    mcpGallery: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return mount(<McpScreen />, s);
}
type R = ReturnType<typeof show>;
const cardOf = (r: R, id: string) => waitFor(() => must(r.$(`[data-testid="connector"][data-id="${id}"]`)) as HTMLElement, r);

const GH = galleryCard({
  id: 'gh',
  display: 'GitHub',
  publisher: 'GitHub',
  brand: 'github',
  auth: 'bearer',
  tokenHelp: 'A fine-grained personal access token.',
  integration: 'github',
  summary: 'Pull requests, issues, Actions runs and code of your repositories.',
});
const LINEAR = galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [] });
const CFDOCS = galleryCard({ id: 'cfdocs', display: 'Cloudflare Documentation', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare' });
const CFAPI = galleryCard({ id: 'cfapi', display: 'Cloudflare API', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare', auth: 'oauth' });
const AIKIDO = galleryCard({
  id: 'aikido',
  display: 'Aikido (local program)',
  publisher: 'Aikido Security',
  brand: 'aikido',
  transport: 'stdio',
  url: undefined,
  auth: 'env',
  secretEnv: 'AIKIDO_API_KEY',
  template: true,
  license: 'AGPL-3.0',
  command: ['npx', '-y', '@aikidosec/mcp@1.0.28'],
});

describe('the MCP connector gallery', () => {
  it('shows every known server as a card with logo, name, publisher, what it gives agents, sign-in kind and state, without a project', async () => {
    const r = show({ mcpGallery: async () => [galleryCard(), GH, LINEAR, CFDOCS, CFAPI, AIKIDO] }, { projects: [] });
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[data-brand="microsoft"]')).not.toBe(null);
    expect(ml.querySelector('h4')!.textContent).toBe('Microsoft Learn');
    expect(ml.textContent).toContain('Microsoft · official');
    expect(ml.textContent).toContain('Search and read official Microsoft');
    expect(ml.textContent).toContain('No account needed');
    expect(ml.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Not connected');
    expect(ml.querySelector('a[target="_blank"]')!.getAttribute('href')).toBe('https://github.com/MicrosoftDocs/mcp');
    // grouped: hosted, then the Cloudflare group, then the programs you run yourself
    const heads = Array.from(r.container.querySelectorAll('[data-testid="connector-gallery"] section h3')).map((h) => h.textContent);
    expect(heads).toEqual(['Hosted servers', 'Cloudflare', 'Programs you run on this computer']);
    const aik = await cardOf(r, 'aikido');
    expect(aik.textContent).toContain('npx -y @aikidosec/mcp@1.0.28');
    expect(aik.textContent).toContain('License AGPL-3.0');
    // the Integrations screen is linked, not edited
    const lin = await cardOf(r, 'linear');
    expect(lin.textContent).toContain('Drydock also imports Linear issues: Integrations');
    expect(lin.querySelector('a[href="/integrations"]')).not.toBe(null);
    // no project yet: connecting still works, putting it in a project waits
    expect(r.container.textContent).toContain('No project selected');
  });

  it('connects a server that needs no account with one click', async () => {
    const calls: unknown[][] = [];
    let cards: McpGalleryCard[] = [galleryCard()];
    const r = show({
      mcpGallery: async () => cards,
      mcpConnect: async (...a: unknown[]) => {
        calls.push(a);
        cards = [galleryCard({ status: 'connected', libraryId: 'mslearn', toolsListed: true, serverInfo: { name: 'learn-mcp', title: 'Microsoft Learn', version: '1.2' } })];
        return { card: cards[0]! };
      },
    });
    const ml = await cardOf(r, 'mslearn');
    await r.act(() => byText(ml, 'button', 'Connect Microsoft Learn').click());
    await waitFor(() => expect(calls).toEqual([['mslearn', undefined]]), r);
    const after = await waitFor(() => {
      const c = r.$('[data-id="mslearn"]') as HTMLElement;
      if (c.dataset.status !== 'connected') throw new Error('not yet');
      return c;
    }, r);
    expect(after.querySelector('[data-testid="connector-account"]')!.textContent).toContain('the server says it is Microsoft Learn (learn-mcp 1.2)');
  });

  it('asks for a token, sends it once, and never shows it again', async () => {
    const calls: unknown[][] = [];
    let cards: McpGalleryCard[] = [GH];
    const r = show({
      mcpGallery: async () => cards,
      mcpConnect: async (...a: unknown[]) => {
        calls.push(a);
        cards = [{ ...GH, status: 'connected', libraryId: 'gh', account: 'octocat' }];
        return { card: cards[0]! };
      },
    });
    const gh = await cardOf(r, 'gh');
    await r.act(() => byText(gh, 'button', 'Connect GitHub').click());
    expect(calls.length).toBe(0); // the token comes first
    const field = byLabel<HTMLInputElement>(gh, 'Token for GitHub');
    expect(field.type).toBe('password');
    expect(gh.textContent).toContain('A fine-grained personal access token.');
    type(field, 'ghp_secret_value_123');
    await r.act(() => (gh.querySelector('[data-testid="connector-token"] button[type="submit"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['gh', 'ghp_secret_value_123']]), r);
    const after = await waitFor(() => {
      const c = r.$('[data-id="gh"]') as HTMLElement;
      if (c.dataset.status !== 'connected') throw new Error('not yet');
      return c;
    }, r);
    expect(after.querySelector('[data-testid="connector-account"]')!.textContent).toContain('Connected as octocat');
    expect(r.container.innerHTML).not.toContain('ghp_secret_value_123');
  });

  it('starts the browser sign-in for an OAuth server and links the sign-in page', async () => {
    let cards: McpGalleryCard[] = [LINEAR];
    const r = show({
      mcpGallery: async () => cards,
      mcpConnect: async () => {
        cards = [{ ...LINEAR, status: 'needs_sign_in', libraryId: 'linear', detail: 'Sign in with your Linear account to finish connecting.' }];
        return { card: cards[0]!, oauth: { id: 'linear', state: 'waiting', url: 'https://linear.app/oauth/authorize?x=1', signedIn: false } };
      },
      mcpOAuthStatus: async () => ({ id: 'linear', state: 'waiting', url: 'https://linear.app/oauth/authorize?x=1', signedIn: false }),
    });
    const lin = await cardOf(r, 'linear');
    await r.act(() => byText(lin, 'button', 'Connect Linear').click());
    const link = await waitFor(() => must(r.$('[data-id="linear"] [data-testid="oauth-link"]')) as HTMLAnchorElement, r);
    expect(link.getAttribute('href')).toBe('https://linear.app/oauth/authorize?x=1');
    expect(link.getAttribute('target')).toBe('_blank');
    expect((r.$('[data-id="linear"] [data-testid="connector-status"]') as HTMLElement).textContent).toBe('Needs sign-in');
  });

  it('says why a server failed and offers to try again', async () => {
    const tries: unknown[][] = [];
    const r = show({
      mcpGallery: async () => [galleryCard({ status: 'error', libraryId: 'mslearn', detail: 'could not reach Microsoft Learn: connection refused' })],
      mcpLibDiscover: (async (...a: unknown[]) => (tries.push(a), libServer())) as never,
    });
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[role="alert"]')!.textContent).toContain('connection refused');
    expect(ml.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Error');
    await r.act(() => byText(ml, 'button', 'Try again').click());
    await waitFor(() => expect(tries).toEqual([['mslearn']]), r);
  });

  it('adds a connected server to a project, grants it to an agent, and disconnects it', async () => {
    const adds: unknown[][] = [];
    const grants: unknown[][] = [];
    const deletes: unknown[][] = [];
    const r = show(
      {
        mcpGallery: async () => [galleryCard({ status: 'connected', libraryId: 'mslearn', projects: ['prj_1'] })],
        mcpServerAdd: (async (...a: unknown[]) => (adds.push(a), projServer())) as never,
        mcpServerGrant: (async (...a: unknown[]) => (grants.push(a), projServer())) as never,
        mcpLibDelete: (async (...a: unknown[]) => void deletes.push(a)) as never,
      },
      { projects: [{ id: 'prj_1', name: 'gocalc' }, { id: 'prj_2', name: 'Beta' }] },
    );
    const ml = await cardOf(r, 'mslearn');
    expect(ml.textContent).toContain('In: gocalc.');
    const sel = await waitFor(() => byLabel<HTMLSelectElement>(ml, 'Project to add Microsoft Learn to'), r);
    expect(Array.from(sel.options).map((o) => o.textContent)).toEqual(['Beta']);
    await r.act(() => byText(ml, 'button', 'Add to project').click());
    await waitFor(() => expect(adds).toEqual([['prj_2', 'mslearn']]), r);
    await r.act(() => byText(ml, 'button', 'Grant to agents').click());
    const box = await waitFor(() => byLabel<HTMLInputElement>(ml, 'Pay may use Microsoft Learn'), r);
    await r.act(() => box.click());
    await waitFor(() => expect(grants).toEqual([['prj_1', 'mslearn', 'agt_1', true]]), r);
    await r.act(() => byText(ml, 'button', 'Disconnect').click());
    await r.act(() => byText(ml, 'button', 'Yes, disconnect').click());
    await waitFor(() => expect(deletes).toEqual([['mslearn']]), r);
  });

  it('shows your own servers by what they say about themselves, and imports a claude_desktop_config.json', async () => {
    const saves: unknown[] = [];
    const discovered: unknown[] = [];
    const r = show({
      mcpLibList: async () => [
        libServer({
          id: 'mine',
          display: 'My tracker',
          serverInfo: { name: 'acme-tracker', title: 'Acme Tracker', version: '3.2.1', instructions: 'Use search first.' },
        }),
      ],
      mcpLibSave: (async (i: unknown) => (saves.push(i), libServer())) as never,
      mcpLibDiscover: (async (id: unknown) => (discovered.push(id), libServer())) as never,
    });
    const own = await waitFor(() => must(r.$('[data-testid="own-server"][data-id="mine"]')) as HTMLElement, r);
    expect(own.querySelector('[data-testid="own-server-identity"]')!.textContent).toBe('Identifies itself as Acme Tracker (acme-tracker 3.2.1)');
    expect(own.textContent).toContain('Use search first.');

    await r.act(() => byText(r.container, 'button', 'Import from a file').click());
    const panel = await waitFor(() => must(document.querySelector('[data-testid="mcp-import"]')) as HTMLElement, r);
    type(byLabel<HTMLTextAreaElement>(panel, 'Configuration (JSON)'), JSON.stringify({ mcpServers: { docs: { url: 'https://learn.microsoft.com/api/mcp' }, gh: { command: 'github-mcp-server', args: ['stdio'], env: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } } } }));
    await r.act(() => byText(panel, 'button', 'Read servers').click());
    await waitFor(() => expect(panel.querySelectorAll('[data-testid="import-candidate"]').length).toBe(2), r);
    expect(panel.textContent).toContain('GITHUB_PERSONAL_ACCESS_TOKEN) are kept in the credential store');
    await r.act(() => byText(panel, 'button', 'Import 2 servers').click());
    await waitFor(() => expect(discovered).toEqual(['docs', 'gh']), r);
    expect(saves).toEqual([
      { id: 'docs', display: 'docs', transport: 'http', url: 'https://learn.microsoft.com/api/mcp' },
      { id: 'gh', display: 'gh', transport: 'stdio', argv: ['github-mcp-server', 'stdio'], secretEnv: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } },
    ]);
  });

  it('groups and names identities as plain text', () => {
    expect(galleryGroups([AIKIDO, CFDOCS, galleryCard()]).map((g) => [g.title, g.cards.map((c) => c.id)])).toEqual([
      ['Hosted servers', ['mslearn']],
      ['Cloudflare', ['cfdocs']],
      ['Programs you run on this computer', ['aikido']],
    ]);
    expect(identityLine(undefined)).toBe('');
    expect(identityLine({ name: 'x', version: '1' })).toBe('x 1');
    expect(identityLine({ name: 'x', title: 'X Server' })).toBe('X Server (x)');
  });
});
