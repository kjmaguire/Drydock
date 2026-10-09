// Providers (a workspace screen): the model endpoints Drydock may call directly, each with its health, data class, key
// state (never the key), a probe, its catalog and a details dialog for settings, prices and quotas; then the form to add one.

import { css, on, type Handle } from 'remix/component';
import { Activity, Cloud, List, Network, Server, Settings2, Trash2 } from 'lucide';
import type { ProbeResult, Provider, ProviderModel } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { card, Page, Section, spacer } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { ProviderDetailPanel } from './provider-detail.tsx';
import { ProviderForm } from './provider-form.tsx';
import { HealthBadge, ModelTable, ProbeView } from './provider-parts.tsx';
import { healthTone, isZeroTime } from './providers.ts';

export { ModelTable, PriceSourceBadge, ProbeView } from './provider-parts.tsx';
export { ProviderDetailPanel } from './provider-detail.tsx';

const KIND_ICON: Record<string, IconNode> = { aggregator: Network, direct: Cloud, host: Server };

const cardCss = css({ display: 'grid', gap: t.s4, padding: '0', overflow: 'hidden' });
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
  padding: `${t.s3} ${t.s4} 0`,
  '& h3': { fontSize: t.textLg, fontWeight: '600', minWidth: '0', overflowWrap: 'anywhere' },
  '& .badges': { display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' },
});
const kindTile = css({ display: 'grid', placeItems: 'center', width: '32px', height: '32px', flex: 'none', borderRadius: t.rMd });
const bodyCss = css({ display: 'grid', gap: t.s3, padding: `0 ${t.s4}` });
const factsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))',
  gap: `${t.s3} ${t.s4}`,
  margin: '0',
  '& > div': { display: 'grid', gap: '2px', minWidth: '0' },
  '& dt': { color: t.textMuted, fontSize: t.textXs, fontWeight: '500', textTransform: 'uppercase', letterSpacing: '0.04em' },
  '& dd': { margin: '0', fontSize: t.textMd, overflowWrap: 'anywhere' },
  '& .url': { gridColumn: '1 / -1' },
});
const trainableCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.warningSoft,
  fontSize: t.textMd,
});
const footCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  background: t.bgRaised,
});
const outCss = css({ display: 'grid', gap: t.s3, padding: `0 ${t.s4}`, '&:empty': { display: 'none' } });

function ProviderCard(handle: Handle<{ p: Provider; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let probe: ProbeResult | undefined;
  let models: ProviderModel[] | undefined;
  let confirmDelete = false;
  let detail = false;
  const redraw = () => void handle.update();

  return () => {
    const { p, onChanged } = handle.props;
    const busy = act.busy !== '';
    const tn = healthTone(p.status);
    return (
      <article mix={[card, cardCss]} data-testid="provider" data-name={p.name}>
        <header mix={headCss}>
          <span mix={kindTile} aria-hidden="true" style={{ background: tone[tn].bg, color: tone[tn].fg }}>
            <Icon icon={KIND_ICON[p.kind] ?? Server} size={16} />
          </span>
          <h3>{p.name}</h3>
          <span class="badges">
            <Badge>{p.kind}</Badge>
            <HealthBadge status={p.status} />
            <Badge tone={p.enabled ? 'ok' : 'muted'}>{p.enabled ? 'enabled' : 'disabled'}</Badge>
            {p.dataClass === 'trainable' ? (
              <Badge tone="warn" title="Public documentation questions only">
                trainable
              </Badge>
            ) : (
              <Badge tone="ok" title="May receive repository content">
                private
              </Badge>
            )}
          </span>
        </header>
        <div mix={bodyCss}>
          {p.dataClass === 'trainable' ? (
            <p mix={trainableCss} data-testid="trainable-note">
              <Badge tone="warn">trainable</Badge> Public documentation questions only; never receives repo content.
            </p>
          ) : null}
          <dl mix={factsCss}>
            <div class="url">
              <dt>Base URL</dt>
              <dd>
                <code>{p.baseUrl}</code>
              </dd>
            </div>
            <div>
              <dt>Tier</dt>
              <dd>{p.tier}</dd>
            </div>
            <div>
              <dt>Flavor</dt>
              <dd>{p.flavor || 'not detected'}</dd>
            </div>
            <div>
              <dt>API key</dt>
              <dd>{p.keyRef ? 'stored in the credential store (never shown)' : 'none'}</dd>
            </div>
            <div>
              <dt>Transport</dt>
              <dd>
                {p.tlsMode}
                {p.lanUnencrypted ? ' (plain http on a private network)' : ''}
              </dd>
            </div>
            <div>
              <dt>Last seen</dt>
              <dd>{isZeroTime(p.lastSeen) ? 'never' : formatDateTime(p.lastSeen)}</dd>
            </div>
            {p.maxConcurrent > 0 ? (
              <div>
                <dt>Max concurrent</dt>
                <dd>{p.maxConcurrent}</dd>
              </div>
            ) : null}
            {Object.entries(p.attestations ?? {}).map(([k, val]) => (
              <div key={k}>
                <dt>{k}</dt>
                <dd>{val ? 'attested' : 'not attested'}</dd>
              </div>
            ))}
          </dl>
          <ErrorNote error={act.error} what="Failed" />
        </div>
        {probe || models ? (
          <div mix={outCss}>
            {probe ? <ProbeView r={probe} /> : null}
            {models ? <ModelTable models={models} /> : null}
          </div>
        ) : null}
        <footer mix={footCss}>
          <Button
            size="sm"
            icon={Activity}
            disabled={busy}
            mix={on('click', () =>
              act.run('test', async () => {
                probe = await s.client.providerTest(p.id);
                onChanged();
              }),
            )}
          >
            {act.busy === 'test' ? 'Testing...' : 'Test'}
          </Button>
          <Button
            size="sm"
            icon={List}
            disabled={busy}
            mix={on('click', () =>
              act.run('models', async () => {
                models = await s.client.providerModels(p.id);
              }),
            )}
          >
            Models
          </Button>
          <Button
            size="sm"
            icon={Settings2}
            aria-haspopup="dialog"
            aria-expanded={detail}
            mix={on('click', () => {
              detail = true;
              redraw();
            })}
          >
            Details and editing
          </Button>
          <span mix={spacer} />
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            mix={on('click', () =>
              act.run('toggle', async () => {
                await s.client.providerSetEnabled(p.id, !p.enabled);
                onChanged();
              }),
            )}
          >
            {p.enabled ? 'Disable' : 'Enable'}
          </Button>
          {confirmDelete ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={busy}
                mix={on('click', () =>
                  act.run('delete', async () => {
                    await s.client.providerDelete(p.id);
                    onChanged();
                  }),
                )}
              >
                Yes, delete {p.name}
              </Button>
              <Button
                size="sm"
                mix={on('click', () => {
                  confirmDelete = false;
                  redraw();
                })}
              >
                Keep
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              icon={Trash2}
              mix={on('click', () => {
                confirmDelete = true;
                redraw();
              })}
            >
              Delete
            </Button>
          )}
        </footer>
        <Dialog
          open={detail}
          wide
          title={p.name}
          onClose={() => {
            detail = false;
            redraw();
          }}
        >
          <ProviderDetailPanel id={p.id} onChanged={onChanged} />
        </Dialog>
      </article>
    );
  };
}

const introCss = css({ color: t.textMuted, fontSize: t.textMd, maxWidth: '820px' });
const listCss = css({ display: 'grid', gap: t.s3 });

export function ProvidersScreen(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.providerList(), when: (e) => e.type.startsWith('provider.') });

  return () => {
    const list = q.data ?? [];
    const up = list.filter((p) => p.status === 'up').length;
    return (
      <Page title="Providers" icon={Network} subtitle={q.data ? `${list.length} configured, ${up} up` : undefined}>
        <p mix={introCss}>
          Model providers Drydock may call directly. API keys are stored in the Windows Credential Manager and are never shown again after you save them. Plain http is
          accepted only for hosts on your own machine or private network.
        </p>
        <ErrorNote error={q.error} what="Could not list providers" />
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="No providers yet." icon={Network}>
              <p mix={muted}>Add an aggregator, a vendor&apos;s API or a server you run with the form below.</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <div mix={listCss}>
            {list.map((p) => (
              <ProviderCard key={p.id} p={p} onChanged={() => q.reload()} />
            ))}
          </div>
        ) : null}
        <Section title="Add a provider" description="The core checks the address: https anywhere, plain http only on loopback or a private network.">
          <ProviderForm onCreated={() => q.reload()} />
        </Section>
      </Page>
    );
  };
}
