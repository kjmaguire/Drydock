// Integrations (decisions P42, P43, P47, P59 and P98): one workspace page for everything Drydock connects to outside itself, as a
// list and the chosen row's detail (ui/master-detail.tsx). The list is grouped: Services (app sync: Drydock's own connection to
// Linear, Notion, Sentry, Aikido and GitHub, whose row opens its own page), Agent tools (the vendors that only have MCP servers)
// and Your servers (the MCP servers you added yourself: a web address or a program on this PC; they used to be in the Library).
// Each row says in a word whether it is On, needs setting up, or needs you; the filters All, On and Needs you count them. The
// chosen row is in the address (/integrations/<key>, replaced as the choice moves) and /integrations/<key> opens it. A detail is
// rows: a service's setup or connection (service-detail.tsx), a vendor's servers and skills (vendor-detail.tsx), one of your
// servers (server-detail.tsx). Nothing of a single project is chosen here (P51): what a project imports, its items, rules and
// writes back, adding a server to it and granting it to its agents are on the project's Integrations page
// (project-integrations-screen.tsx), which the projects rows open. /mcp is an alias of this page.

import { css, navigate, on, type Handle } from 'remix/component';
import { Blocks, Globe, Plug, Plus, SearchX } from 'lucide';
import type { IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard, McpLibraryServer } from '../../api/mcpClient.ts';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { AddRow, MasterDetail, type ListGroup, type ListItem } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { AddMcpServer } from '../mcp/gallery.tsx';
import { importedNote } from '../mcp/import.ts';
import { serverLogo } from '../mcp/mcp-screen.tsx';
import {
  filterCounts,
  KINDS,
  ownServers,
  ownServerState,
  ownServerSub,
  PROVIDERS_UNREAD,
  providerRowState,
  serverIdOf,
  serverKey,
  showsUnder,
  tileState,
  vendors,
  vendorState,
  vendorSub,
  type RowState,
  type Vendor,
} from './integrations.ts';
import { ServerDetail } from './server-detail.tsx';
import type { Open } from './service-detail.tsx';
import { VendorPanel } from './vendor-detail.tsx';
import { openRouterProviders, providerBrand } from '../providers/providers.ts';
import { OpenRouterPanel } from './openrouter-panel.tsx';

// the GitHub settings page shows GitHub's detail with the component this page opens (screens/github/github-screen.tsx)
export { VendorDetail, VendorPanel } from './vendor-detail.tsx';

const noteCss = css({ '&:empty': { display: 'none' } });
const listNote = css({ margin: `${t.s3} ${t.s2} 0`, fontSize: t.textSm, '& a': { whiteSpace: 'nowrap' } });

/** OpenRouter's row and address (/integrations/openrouter): a model provider, set up here, owned by Models (decision P103). */
const OPENROUTER = 'openrouter';

/** One row with what the page needs to filter it and draw its detail. */
interface Row {
  item: ListItem;
  state: RowState;
}

/**
 * The workspace's Integrations page. `initial` is the row the address names (/integrations/<key>); the page stays mounted while
 * it changes. `go` replaces the address when the choice moves; `open` goes to another page (a project's Integrations page).
 */
export function IntegrationsScreen(handle: Handle<{ initial?: string; go?: (href: string) => void; open?: Open }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  // the catalog changes when an OAuth app is registered or removed (integration.oauth_app_changed)
  const kinds = query(handle, { load: () => s.client.integrationKinds(), when: (e) => e.type === 'integration.oauth_app_changed' });
  const gallery = query(handle, { load: () => s.client.mcpGallery(), when: (e) => e.type.startsWith('mcp.server') });
  const lib = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  // the model providers, for OpenRouter's row (decision P103): Models owns them, this page shows OpenRouter among the integrations
  const providers = query(handle, { load: () => s.client.providerList(), when: (e) => e.type.startsWith('provider.') });
  // the registries are read once, and only when a vendor on the page publishes some
  const regs = query(handle, { load: () => s.client.skillRegistries(), enabled: () => (gallery.data ?? []).some((c) => (c.skillRegistries ?? []).length > 0) });
  let selected = handle.props.initial ?? '';
  let seenInitial = handle.props.initial;
  let filter = 'all';
  let adding = false;
  let note = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const go = (href: string) => (handle.props.go ? handle.props.go(href) : void navigate(href, { history: 'replace' }));
  const open: Open = (href) => (handle.props.open ? handle.props.open(href) : void navigate(href));
  const choose = (key: string) => {
    // the row already in the address; the first row, shown while nothing is chosen, goes into the address when it is clicked
    if (key === selected) return;
    set(() => ((selected = key), (note = '')));
    go(routes.integrations.href({ key }));
  };
  const reloadServers = () => {
    lib.reload();
    gallery.reload();
  };

  return () => {
    // the address moved (a link, Back): follow it
    if (handle.props.initial !== seenInitial) {
      seenInitial = handle.props.initial;
      selected = handle.props.initial ?? '';
    }
    const list: IntegrationView[] = q.data ?? [];
    const cards: McpGalleryCard[] = gallery.data ?? [];
    const servers: McpLibraryServer[] = lib.data ?? [];
    const all = vendors(KINDS, cards);
    const mine = ownServers(servers, cards);
    // the providers too (OpenRouter's row): its row and the count would say Set up until they arrive
    const loaded = !!q.data && !!gallery.data && (!!providers.data || !!providers.error);

    const vendorRow = (v: Vendor): Row => {
      const conn = v.kind ? tileState(list, v.key) : undefined;
      const state = vendorState(v, conn);
      const data: Record<string, string> = { kind: v.key };
      if (v.kind) data.connected = conn ? 'true' : 'false';
      else data.mcp = 'true';
      return {
        state,
        item: {
          key: v.key,
          title: v.label,
          logo: v.logo,
          icon: Plug,
          sub: vendorSub(v, conn),
          status: { text: state.text, tone: state.tone },
          // GitHub has a page of its own: its sign-in, connection, server and CI runners
          href: v.key === 'github' ? routes.github.href() : undefined,
          testId: 'integration-row',
          data,
        },
      };
    };
    const serverRow = (x: McpLibraryServer): Row => {
      const state = ownServerState(x);
      return {
        state,
        item: {
          key: serverKey(x.id),
          title: x.display,
          logo: serverLogo(x) || undefined,
          icon: x.transport === 'http' ? Globe : Plug,
          sub: ownServerSub(x),
          status: { text: state.text, tone: state.tone },
          testId: 'integration-row',
          data: { server: x.id },
        },
      };
    };
    const services_ = all.filter((v) => v.kind).map(vendorRow);
    const tools = all.filter((v) => !v.kind).map(vendorRow);
    const own = mine.map(serverRow);
    const orMine = providers.data ? openRouterProviders(providers.data) : undefined;
    const orState = orMine ? providerRowState(orMine) : PROVIDERS_UNREAD;
    const modelRows: Row[] = [
      {
        state: orState,
        item: {
          key: OPENROUTER,
          title: 'OpenRouter',
          logo: 'openrouter',
          icon: Plug,
          sub: 'Models from many vendors, one key',
          status: { text: orState.text, tone: orState.tone },
          testId: 'integration-row',
          data: { kind: OPENROUTER, provider: 'true', connected: String(orState.bucket !== 'setup') },
        },
      },
    ];
    const rows = [...services_, ...tools, ...modelRows, ...own];
    const counts = filterCounts(rows.map((r) => r.state));
    const shown = (rs: Row[]) => rs.filter((r) => showsUnder(filter, r.state)).map((r) => r.item);
    const groups: ListGroup[] = [
      { key: 'services', title: 'Services', note: 'app sync', items: shown(services_) },
      ...(tools.length > 0 ? [{ key: 'tools', title: 'Agent tools', note: 'MCP', items: shown(tools) }] : []),
      { key: 'models', title: 'Model providers', note: 'Models', items: shown(modelRows) },
      {
        key: 'servers',
        title: 'Your servers',
        note: 'MCP',
        items: shown(own),
        footer: <AddRow label="Add a server" onClick={() => set(() => (adding = true))} testId="add-server-row" />,
      },
    ];
    // nothing chosen yet: the first row that has a detail here (not GitHub, whose row opens its page)
    const firstKey = rows.find((r) => !r.item.href)?.item.key ?? '';
    // the row whose detail shows: the chosen one, else the first (drawn, not put in the address)
    const key = selected || firstKey;
    const serverId = serverIdOf(key);
    const vendor = serverId || key === OPENROUTER ? undefined : all.find((v) => v.key === key);
    const server = serverId ? servers.find((x) => x.id === serverId) : undefined;

    const detail =
      (q.loading && !q.data) || (gallery.loading && !gallery.data) || (serverId && lib.loading && !lib.data) ? (
        <Loading />
      ) : vendor ? (
        <VendorPanel
          key={vendor.key}
          v={vendor}
          info={(kinds.data ?? []).find((k) => k.kind === vendor.key)}
          conn={vendor.kind ? tileState(list, vendor.key) : undefined}
          registries={regs.data}
          registriesLoading={regs.loading}
          registriesError={regs.error}
          onSyncChanged={q.reload}
          onAgentsChanged={reloadServers}
          onKindsChanged={kinds.reload}
          open={open}
          footer={
            vendor.key === 'github' ? (
              <>
                Everything Drydock does with GitHub, with your gh sign-in and the CI runners, is on the <a href={routes.github.href()} data-testid="github-page-link">GitHub</a> page.
              </>
            ) : undefined
          }
        />
      ) : key === OPENROUTER ? (
        <OpenRouterPanel
          mine={orMine}
          state={orState}
          loading={providers.loading}
          error={providers.error}
          onCreated={(made) => {
            // the list follows on provider.registered; a provider pointed elsewhere is in Models, not here
            set(
              () =>
                (note =
                  !made || providerBrand(made) === 'openrouter'
                    ? 'OpenRouter is connected. Its page in Models has its models and key limits.'
                    : `${made.name} was added in Models. Its address is not openrouter.ai, so it is not OpenRouter's row here.`),
            );
          }}
        />
      ) : server ? (
        <ServerDetail
          key={server.id}
          s={server}
          onChanged={reloadServers}
          onRemoved={(name) => {
            set(() => {
              note = `${name} was removed.`;
              selected = firstKey;
            });
            go(routes.integrations.href({ key: firstKey }));
            reloadServers();
          }}
        />
      ) : loaded ? (
        <Empty title="Nothing here by that name" icon={SearchX}>
          <p>It may have been removed. Choose an integration from the list.</p>
        </Empty>
      ) : null;

    return (
      <Page
        title="Integrations"
        icon={Blocks}
        flush
        guide="integrations"
        testId="integrations"
        subtitle={loaded ? `Connected once for the whole workspace · ${counts.on} connected` : 'Connected once for the whole workspace'}
        actions={
          <Button variant="primary" size="sm" icon={Plus} mix={[on('click', () => set(() => (adding = true)))]}>
            Add MCP server
          </Button>
        }
      >
        <MasterDetail
          label="All integrations"
          testId="connector-gallery"
          address={handle.props.initial}
          groups={loaded ? groups : []}
          selected={key}
          onSelect={choose}
          search="Filter integrations"
          filters={[
            { key: 'all', label: 'All', count: counts.all },
            { key: 'on', label: 'On', count: counts.on },
            { key: 'needs', label: 'Needs you', count: counts.needs },
          ]}
          filter={filter}
          onFilter={(f) => set(() => (filter = f))}
          listFooter={
            <>
              <ErrorNote error={q.error ?? kinds.error ?? gallery.error ?? lib.error ?? providers.error} what="Integrations" />
              {!loaded && !q.error && !gallery.error ? <Loading /> : null}
              {loaded && filter !== 'all' && groups.every((g) => g.items.length === 0) ? (
                <p mix={[faint, listNote]} data-testid="list-empty">
                  {filter === 'on' ? 'Nothing is on yet.' : 'Nothing needs you.'}
                </p>
              ) : null}
              <p mix={[faint, listNote]} data-testid="gateway-note">
                Agent CLIs other than Claude Code get these servers once you allow it under MCP gateway on <a href={routes.agents.href()}>Agents</a>.
              </p>
            </>
          }
          detail={
            <>
              <div role="status" aria-live="polite" data-testid="integrations-note" mix={noteCss}>
                {note ? <Notice tone="ok">{note}</Notice> : null}
              </div>
              {detail}
            </>
          }
        />
        <Dialog open={adding} title="Add an MCP server" wide onClose={() => set(() => (adding = false))}>
          {adding ? (
            <AddMcpServer
              existingIds={servers.map((x) => x.id)}
              onCancel={() => set(() => (adding = false))}
              onSaved={(saved) => {
                set(() => {
                  adding = false;
                  note = `${saved.display} is connected. Press Find tools, then add it to a project on that project's Integrations page.`;
                  selected = serverKey(saved.id); // the new server's detail: Find tools is the next step
                });
                go(routes.integrations.href({ key: serverKey(saved.id) }));
                reloadServers();
              }}
              onImported={(n) => {
                set(() => {
                  adding = false;
                  note = importedNote(n, 'choose it under Your servers and press Find tools');
                });
                reloadServers();
              }}
            />
          ) : null}
        </Dialog>
      </Page>
    );
  };
}
