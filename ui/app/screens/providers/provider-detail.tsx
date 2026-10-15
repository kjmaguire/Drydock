// A provider in full (docs/ui.md 9): settings to edit, served models with editable prices, quota windows, OpenRouter key
// limits and, for OpenRouter, the endpoints its calls skip for poor recent uptime (decision P53 c). The API key is
// write-only here: it can be replaced, never read back or shown.

import { css, on, ref, type Handle } from 'remix/component';
import { KeyRound } from 'lucide';
import type { EndpointUptimeView, ProviderEdit } from '../../api/providerClient.ts';
import type { Provider, ProviderModel } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { tableCard } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { Meter } from '../budgets/meter.tsx';
import { numOrUndef } from '../shared/budget-pause.tsx';
import { avoidedEndpointText, keyInfoText, QUOTA_KINDS, quotaFraction, quotaKindLabel, rawPrice, uptimeThreshold } from './providers.ts';

const sectionCss = css({ display: 'grid', gap: t.s3, '& > h3': { fontSize: t.textMd, fontWeight: '600' } });
const panelCss = css({ display: 'grid', gap: t.s6 });

/** Setup reads the provider once (like a form's initial values); after a save the fields keep what was saved. */
function SettingsForm(handle: Handle<{ d: Provider; uptime?: EndpointUptimeView; busy: string; onSave: (e: ProviderEdit) => void }>) {
  const d0 = handle.props.d;
  let minUp = handle.props.uptime ? String(handle.props.uptime.minUptime) : '';
  let name = d0.name;
  let baseUrl = d0.baseUrl;
  let tier = d0.tier;
  let dataClass = d0.dataClass;
  let maxc = String(d0.maxConcurrent || '');
  // The replacement key lives only in the (uncontrolled) input: read once on submit and cleared at once.
  let keyInput: HTMLInputElement | undefined;

  const text = (fn: (v: string) => void) =>
    on('input', (e: Event & { currentTarget: HTMLInputElement }) => {
      fn(e.currentTarget.value);
      void handle.update();
    });
  const choose = (fn: (v: string) => void) =>
    on('change', (e: Event & { currentTarget: HTMLSelectElement }) => {
      fn(e.currentTarget.value);
      void handle.update();
    });

  return () => {
    const { d, uptime, busy, onSave } = handle.props;
    return (
      <form
        aria-label="Provider settings"
        autocomplete="off"
        mix={[
          formGrid,
          on('submit', (e) => {
            e.preventDefault();
            const apiKey = keyInput?.value ?? '';
            if (keyInput) keyInput.value = '';
            onSave({
              id: d.id,
              name,
              baseUrl,
              tier,
              dataClass,
              maxConcurrent: numOrUndef(maxc) ?? 0,
              apiKey: apiKey || undefined,
              minEndpointUptime: uptime ? uptimeThreshold(minUp) : undefined,
            });
          }),
        ]}
      >
        <Field label="Name">{(id) => <input id={id} value={name} required mix={text((v) => (name = v))} />}</Field>
        <Field label="Base URL">{(id) => <input id={id} value={baseUrl} required spellcheck={false} mix={text((v) => (baseUrl = v))} />}</Field>
        <Field label="Tier">
          {(id) => (
            <select id={id} value={tier} mix={choose((v) => (tier = v))}>
              {['premium', 'cheap', 'free'].map((x) => (
                <option key={x} value={x} selected={x === tier}>
                  {x}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Data class">
          {(id) => (
            <select id={id} value={dataClass} mix={choose((v) => (dataClass = v))}>
              <option value="private" selected={dataClass === 'private'}>
                private
              </option>
              <option value="trainable" selected={dataClass === 'trainable'}>
                trainable (public documentation questions only)
              </option>
            </select>
          )}
        </Field>
        <Field label="Max concurrent agents (empty: unlimited)">{(id) => <input id={id} inputMode="numeric" value={maxc} mix={text((v) => (maxc = v))} />}</Field>
        {uptime ? (
          <Field label={`Skip endpoints below this recent uptime, % (default ${uptime.defaultMinUptime}; 0 turns it off)`}>
            {(id) => <input id={id} type="number" min="0" max="100" step="any" inputMode="decimal" value={minUp} mix={text((v) => (minUp = v))} />}
          </Field>
        ) : null}
        <Field label="Replace the API key (kept in the credential store; never shown)">
          {(id) => (
            <input
              id={id}
              type="password"
              autocomplete="new-password"
              spellcheck={false}
              placeholder={d.keyRef ? 'a key is stored' : 'no key stored'}
              mix={ref((n: HTMLInputElement) => (keyInput = n))}
            />
          )}
        </Field>
        <div mix={formActions}>
          <Button type="submit" variant="primary" disabled={busy !== ''}>
            {busy === 'save' ? 'Saving...' : 'Save settings'}
          </Button>
        </div>
      </form>
    );
  };
}

const priceInput = css({ width: '96px' });

function PriceRow(handle: Handle<{ m: ProviderModel; onSave: (i: number, o: number) => void }>) {
  let editing = false;
  let i = String(handle.props.m.inputPerM ?? 0);
  let o = String(handle.props.m.outputPerM ?? 0);
  const redraw = () => void handle.update();
  return () => {
    const { m, onSave } = handle.props;
    return (
      <tr data-testid="price-row" data-model={m.id}>
        <td>
          <code>{m.id}</code>
        </td>
        {editing ? (
          <>
            <td>
              <input
                aria-label={`Input price of ${m.id}`}
                inputMode="decimal"
                value={i}
                mix={[priceInput, on('input', (e) => ((i = e.currentTarget.value), redraw()))]}
              />
            </td>
            <td>
              <input
                aria-label={`Output price of ${m.id}`}
                inputMode="decimal"
                value={o}
                mix={[priceInput, on('input', (e) => ((o = e.currentTarget.value), redraw()))]}
              />
            </td>
          </>
        ) : (
          <>
            <td>{rawPrice(m, 'in')}</td>
            <td>{rawPrice(m, 'out')}</td>
          </>
        )}
        <td>
          {m.priceSource || 'unknown'}
          {m.priceSource === 'user' && m.priceUpdatedAt ? <span mix={muted}> {formatDateTime(m.priceUpdatedAt)}</span> : null}
        </td>
        <td>{m.probeStatus || 'not probed'}</td>
        <td style={{ whiteSpace: 'nowrap', textAlign: 'right' }}>
          {editing ? (
            <>
              <Button
                size="sm"
                variant="primary"
                mix={on('click', () => {
                  const a = Number(i);
                  const b = Number(o);
                  if (Number.isFinite(a) && Number.isFinite(b)) {
                    onSave(a, b);
                    editing = false;
                    redraw();
                  }
                })}
              >
                Save price
              </Button>{' '}
              <Button size="sm" variant="ghost" mix={on('click', () => ((editing = false), redraw()))}>
                Cancel
              </Button>
            </>
          ) : (
            <Button size="sm" variant="ghost" mix={on('click', () => ((editing = true), redraw()))}>
              Edit price
            </Button>
          )}
        </td>
      </tr>
    );
  };
}

const quotaForm = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: t.s2,
  alignItems: 'center',
  '& select': { width: 'auto' },
  '& input': { width: '110px' },
});

function QuotaForm(handle: Handle<{ models: string[]; onAdd: (model: string, kind: string, limit: number) => void }>) {
  let model = '';
  let kind = 'rpd';
  let limit = '';
  const redraw = () => void handle.update();
  return () => (
    <form
      aria-label="Set a quota limit"
      mix={[
        quotaForm,
        on('submit', (e) => {
          e.preventDefault();
          const n = Number(limit);
          if (n > 0) handle.props.onAdd(model, kind, n);
        }),
      ]}
    >
      <select aria-label="Quota model" value={model} mix={on('change', (e) => ((model = e.currentTarget.value), redraw()))}>
        <option value="" selected={model === ''}>
          all models
        </option>
        {handle.props.models.map((m) => (
          <option key={m} value={m} selected={m === model}>
            {m}
          </option>
        ))}
      </select>
      <select aria-label="Quota window" value={kind} mix={on('change', (e) => ((kind = e.currentTarget.value), redraw()))}>
        {QUOTA_KINDS.map(([k, l]) => (
          <option key={k} value={k} selected={k === kind}>
            {l}
          </option>
        ))}
      </select>
      <input aria-label="Quota limit" inputMode="decimal" placeholder="limit" value={limit} mix={on('input', (e) => ((limit = e.currentTarget.value), redraw()))} />
      <Button type="submit" size="sm">
        Set limit
      </Button>
    </form>
  );
}

const quotaList = css({
  display: 'grid',
  gap: t.s3,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: '6px', fontSize: t.textMd },
  '& .line': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
});
const keyRow = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' });

/** What Drydock's own calls to this OpenRouter provider skip: endpoints whose recent uptime is below the threshold. */
function EndpointUptimeSection(handle: Handle<{ v: EndpointUptimeView }>) {
  return () => {
    const { v } = handle.props;
    return (
      <div mix={sectionCss} data-testid="endpoint-uptime">
        <h3>Endpoint uptime</h3>
        <p mix={muted}>
          {v.minUptime > 0
            ? `Before a call, Drydock reads the model's endpoints from OpenRouter (kept 5 minutes) and asks OpenRouter to skip those below ${v.minUptime}% recent uptime. When every endpoint is below it, the best are kept; when the lookup fails, the call goes out unchanged. Agent CLIs that call OpenRouter themselves are not affected.`
            : 'Off: calls go to OpenRouter without skipping any endpoint.'}
        </p>
        {v.avoiding.length === 0 ? (
          v.minUptime > 0 ? (
            <p mix={muted}>No endpoint is being skipped.</p>
          ) : null
        ) : (
          <ul mix={quotaList}>
            {v.avoiding.map((a) => (
              <li key={a.model} data-testid="endpoint-avoidance" data-model={a.model}>
                <span class="line">
                  <code>{a.model}</code>
                  {a.keptBest ? (
                    <Badge tone="warn" title="Every endpoint was below the threshold, so the best ones were kept">
                      all below {a.threshold}%: best kept
                    </Badge>
                  ) : null}
                  <span mix={faint}>{formatDateTime(a.at)}</span>
                </span>
                {a.avoided.map((e) => (
                  <span key={e.tag} class="line" data-testid="avoided-endpoint">
                    <Badge tone="err">skipped</Badge> {avoidedEndpointText(e)}
                  </span>
                ))}
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  };
}

export function ProviderDetailPanel(handle: Handle<{ id: string; onChanged: () => void }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.providerDetail(handle.props.id),
    when: (e) => e.type.startsWith('provider.'),
    key: () => handle.props.id,
  });
  const act = action(handle);
  let keyInfo: string | undefined;

  return () => {
    const { id, onChanged } = handle.props;
    const d = q.data;
    if (!d) return q.error ? <ErrorNote error={q.error} what="Could not load the provider" /> : <Loading />;
    return (
      <section mix={panelCss} data-testid="provider-detail" aria-label={`${d.provider.name} details`}>
        <ErrorNote error={act.error ?? q.error} what="Provider" />
        <div mix={sectionCss}>
          <h3>Settings</h3>
          <SettingsForm
            d={d.provider}
            uptime={d.endpointUptime}
            busy={act.busy}
            onSave={async (e) => {
              let ok = false;
              await act.run('save', async () => {
                await s.client.providerUpdate(e);
                ok = true;
              });
              if (ok) {
                q.reload();
                onChanged();
              }
            }}
          />
        </div>
        <div mix={sectionCss}>
          <h3>Models and prices</h3>
          {d.models.length === 0 ? <p mix={muted}>No models are listed yet. Test the provider to fetch its catalog.</p> : null}
          <div mix={tableCard}>
            <table aria-label="Models and prices">
              <thead>
                <tr>
                  <th scope="col">Model</th>
                  <th scope="col">Input $/Mtok</th>
                  <th scope="col">Output $/Mtok</th>
                  <th scope="col">Price from</th>
                  <th scope="col">Probe</th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {d.models.map((m) => (
                  <PriceRow
                    key={m.id}
                    m={m}
                    onSave={async (i, o) => {
                      await act.run('price', () => s.client.providerSetPrice(id, m.id, i, o, m.cacheReadPerM, m.cacheWritePerM));
                      q.reload();
                    }}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </div>
        <div mix={sectionCss}>
          <h3>Quota</h3>
          {d.quota.length === 0 ? (
            <p mix={muted}>No limits known. Drydock never invents one: they come from the provider&apos;s headers or API, or from what you set here.</p>
          ) : (
            <ul mix={quotaList}>
              {d.quota.map((w, i) => {
                const f = quotaFraction(w.used, w.limit);
                return (
                  <li key={i} data-testid="quota-window">
                    <span class="line">
                      <strong title={quotaKindLabel(w.kind)}>{w.kind}</strong> {w.model ? <code>{w.model}</code> : 'all models'}: {w.used} of {w.limit} used{' '}
                      <Badge>{w.source}</Badge>
                      {w.resetAt ? <span mix={faint}> resets {formatDateTime(new Date(w.resetAt).toISOString())}</span> : null}
                    </span>
                    {f !== undefined ? (
                      <Meter value={f} tone={f >= 1 ? 'err' : f >= 0.8 ? 'warn' : 'accent'} label={`${w.kind}: ${w.used} of ${w.limit} used`} />
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
          <QuotaForm
            models={d.models.map((m) => m.id)}
            onAdd={async (model, kind, limit) => {
              await act.run('quota', () => s.client.providerSetQuota(id, model, kind, limit));
              q.reload();
            }}
          />
        </div>
        {d.endpointUptime ? <EndpointUptimeSection v={d.endpointUptime} /> : null}
        <div mix={keyRow}>
          <Button
            icon={KeyRound}
            disabled={act.busy !== ''}
            mix={on('click', async () => {
              const r = await act.run('key', () => s.client.providerDetail(id, true));
              if (r) {
                keyInfo = keyInfoText(r);
                void handle.update();
              }
            })}
          >
            Read the key&apos;s limits from the provider
          </Button>
          {keyInfo !== undefined ? <span data-testid="key-info">{keyInfo}</span> : null}
        </div>
      </section>
    );
  };
}
