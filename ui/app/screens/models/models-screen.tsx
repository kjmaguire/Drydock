// Models (decision P98): where Drydock's own model calls go, on one list-and-detail page (/models, /models/<key>). The list has
// "On this PC" (one row, `this-pc`: the local engine and the model it serves, decisions P65, P95, P97) and "Providers" (one row
// per provider: aggregators, vendor APIs, your own servers, with their health; then "Add a provider", `new`). The core registers
// the local server as a provider of its own (local.get's settings.providerId): that provider is the this-pc row, never a second
// row. Routing chooses among them. The models an agent CLI uses through its own sign-in are on the Agents page, not here.
// Choosing a row puts its key in the address (replacing it); opening /models/<key> opens that row, /models the this-pc row.

import { css, navigate, type Handle } from 'remix/component';
import { Cpu, Plus, Route as RouteIcon } from 'lucide';
import { plural } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { t } from '../../ui/tokens.ts';
import { AddRow, DetailHeader, MasterDetail, type ListGroup, type ListItem } from '../../ui/master-detail.tsx';
import { KIND_ICON, ProviderPane } from '../providers/provider-detail.tsx';
import { ProviderForm } from '../providers/provider-form.tsx';
import { listedProviders, nextAfterRemoval, providerBrand, providerRowSub, providerStatus } from '../providers/providers.ts';
import { localRow, NEW_PROVIDER, THIS_PC } from './models.ts';
import { ThisPcDetail } from './this-pc.tsx';

const newCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });

export function ModelsScreen(
  handle: Handle<{
    /** "now" for the Started line (tests). */
    now?: number;
    /** The row to open (/models/<key>): `this-pc`, `new` or a provider's id. */
    initial?: string;
    /** Replaces the change of address in tests. */
    onNavigate?: (href: string) => void;
  }>,
) {
  const s = services(handle);
  const local = query(handle, {
    load: () => s.client.localGet(),
    when: (e) => e.type === 'local.changed',
    debounceMs: 150,
  });
  const providers = query(handle, { load: () => s.client.providerList(), when: (e) => e.type.startsWith('provider.') });
  // the key the address last gave: a different one (a link, Back) opens its row; a row chosen here changes the address after
  let addressed = handle.props.initial;
  let selected = addressed || THIS_PC;
  let created = ''; // a provider just added: kept chosen until the list has it

  const go = (key: string) => {
    const href = routes.models.href({ key });
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href, { history: 'replace' });
  };
  const choose = (key: string) => {
    if (key === selected) return;
    selected = key;
    go(key);
    void handle.update();
  };

  return () => {
    // a new address (a link, Back) opens its row; the screen stays mounted
    if (handle.props.initial !== addressed) {
      addressed = handle.props.initial;
      selected = addressed || THIS_PC;
    }
    const v = local.data;
    const settled = (v || local.error) && (providers.data || providers.error);
    const all = providers.data ?? [];
    const listed = listedProviders(all, v?.settings?.providerId);
    if (created && listed.some((p) => p.id === created)) created = '';
    // the local server's own provider entry is this row; a provider that is gone (removed elsewhere) gives way to it
    if (settled && v?.settings?.providerId && selected === v.settings.providerId) selected = THIS_PC;
    if (settled && providers.data && selected !== THIS_PC && selected !== NEW_PROVIDER && !created && !listed.some((p) => p.id === selected)) selected = THIS_PC;
    const open = listed.find((p) => p.id === selected);
    const row = v ? localRow(v) : undefined;
    const up = listed.filter((p) => p.enabled && p.status === 'up').length;

    const local_: ListItem = {
      key: THIS_PC,
      title: 'Models on this PC',
      icon: Cpu,
      sub: row?.sub ?? (local.error ? 'Could not be read' : ''),
      status: row ? { text: row.text, tone: row.tone } : undefined,
      testId: 'models-row',
      data: { key: THIS_PC },
    };
    const groups: ListGroup[] = [
      { key: 'this-pc', title: 'On this PC', items: [local_] },
      {
        key: 'providers',
        title: 'Providers',
        items: listed.map(
          (p): ListItem => ({
            key: p.id,
            title: p.name,
            logo: providerBrand(p),
            icon: KIND_ICON[p.kind],
            sub: providerRowSub(p),
            status: providerStatus(p),
            quiet: !p.enabled,
            testId: 'models-row',
            data: { key: p.id, kind: 'provider', name: p.name, status: p.status },
          }),
        ),
        footer: providers.data ? <AddRow label="Add a provider" current={selected === NEW_PROVIDER} onClick={() => choose(NEW_PROVIDER)} testId="provider-add" /> : undefined,
      },
    ];

    const subtitle = settled
      ? [row ? `This PC: ${row.text.toLowerCase()}` : '', providers.data ? `${plural(listed.length, 'provider')}, ${up} up` : ''].filter(Boolean).join(' · ')
      : undefined;

    // the address named a provider and the list could not be read: the detail pane says so, in place of the row that could not be
    // found, so the list footer keeps quiet rather than repeat the same words twice on one screen
    const detailSaysListFailed = selected !== THIS_PC && selected !== NEW_PROVIDER && !providers.data && Boolean(providers.error);

    let detail;
    if (selected === NEW_PROVIDER) {
      detail = (
        <section aria-label="Add a provider" data-testid="provider-new" mix={newCss}>
          <DetailHeader
            title="Add a provider"
            icon={Plus}
            sub={
              <>
                An aggregator, a vendor&apos;s API or your own server; https anywhere, plain http only on this PC or a private network.{' '}
                <LearnMore topic="providers" />
              </>
            }
          />
          <ProviderForm
            onCreated={(p) => {
              if (p?.id) {
                created = p.id;
                choose(p.id);
              }
              providers.reload();
            }}
          />
        </section>
      );
    } else if (selected !== THIS_PC && open) {
      detail = (
        <ProviderPane
          key={open.id}
          p={open}
          onChanged={() => providers.reload()}
          onRemoved={() => {
            choose(nextAfterRemoval(listed, open.id, THIS_PC));
            providers.reload();
          }}
        />
      );
    } else if (detailSaysListFailed) {
      // say so, never wait for a list that is not coming
      detail = <ErrorNote error={providers.error} what="Could not read this provider" />;
    } else if (selected !== THIS_PC) {
      detail = <Loading />;
    } else {
      detail = <ThisPcDetail view={v} loading={local.loading} error={local.error} setView={(next) => local.set(next)} now={handle.props.now} />;
    }

    return (
      <Page
        title="Models"
        icon={Cpu}
        subtitle={subtitle}
        flush
        testId="models-page"
        actions={
          <>
            <a href={routes.routing.href()} mix={buttonStyle('ghost', 'sm')} data-testid="models-routing">
              <Icon icon={RouteIcon} size={14} />
              Routing
            </a>
            <LearnMore topic="providers" label="Guide" />
          </>
        }
      >
        {!settled ? (
          <Loading />
        ) : (
          <MasterDetail
            label="Models and providers"
            groups={groups}
            selected={selected}
            onSelect={choose}
            detail={detail}
            testId="models"
            address={handle.props.initial}
            listFooter={
              <>
                {detailSaysListFailed ? null : <ErrorNote error={providers.error} what="Could not list providers" />}
                {providers.data && listed.length === 0 ? (
                  <Empty title="No providers yet">
                    <p>Add an aggregator, a vendor&apos;s API or a server you run.</p>
                  </Empty>
                ) : null}
              </>
            }
          />
        )}
      </Page>
    );
  };
}
