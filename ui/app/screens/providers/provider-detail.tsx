// A provider on the Models page (decision P98): the head (its logo, name, state, kind and host; Edit, Enable or Disable, Remove),
// then its settings as rows (kind, base URL, data class, tier, the API key, attestations, transport), its health with Check now,
// the models it offers with editable prices, its quota windows, OpenRouter's key limits and, for OpenRouter, the endpoints its
// calls skip for poor recent uptime (decision P53 c). The API key is write-only here: it can be replaced, never read back or shown.

import { css, on, ref, type Handle } from 'remix/component';
import { Cloud, Network, Server } from 'lucide';
import type { EndpointUptimeView, ProviderEdit } from '../../api/providerClient.ts';
import type { ProbeResult, Provider, ProviderModel } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { parseWhole } from '../../lib/limits.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { errorAttrs, Field, formActions, formGrid } from '../../ui/form.tsx';
import type { IconNode } from '../../ui/icon.tsx';
import { tableCard } from '../../ui/layout.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { DetailHeader, SettingRow, SettingsGroup, StatusWord } from '../../ui/master-detail.tsx';
import { Select, selectOptions, selectValues } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { Meter } from '../budgets/meter.tsx';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import { PriceSourceBadge, ProbeView } from './provider-parts.tsx';
import {
  ATTESTATIONS,
  avoidedEndpointText,
  isZeroTime,
  keyInfoText,
  kindWords,
  priceText,
  providerBrand,
  providerHeadSub,
  providerStatus,
  QUOTA_KINDS,
  quotaFraction,
  quotaKindLabel,
  uptimeThreshold,
} from './providers.ts';

/** A provider without a brand logo shows its kind's icon. */
export const KIND_ICON: Record<string, IconNode> = { aggregator: Network, direct: Cloud, host: Server };

const panelCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });
// what a group holds besides its rows (a form, a list, a probe), inside the group's surface
const blockCss = css({ display: 'grid', gap: t.s3, padding: `${t.s3} ${t.s4}`, minWidth: '0', '&:not(:first-child)': { borderTop: `1px solid ${t.border}` }, '& p': { margin: '0' } });

/** What the concurrency limit says when its text is not a whole number (M25): a typo is never saved as "unlimited". */
export const MAX_CONCURRENT_ERROR = 'Enter the most agents that may run at once as a whole number greater than zero, or leave it empty for no limit.';

/** Setup reads the provider once (like a form's initial values); after a save the fields keep what was saved. */
function SettingsForm(handle: Handle<{ d: Provider; uptime?: EndpointUptimeView; busy: string; onSave: (e: ProviderEdit) => void; onCancel: () => void; focusKey?: boolean }>) {
  const d0 = handle.props.d;
  let minUp = handle.props.uptime ? String(handle.props.uptime.minUptime) : '';
  let name = d0.name;
  let baseUrl = d0.baseUrl;
  let tier = d0.tier;
  let dataClass = d0.dataClass;
  let maxc = String(d0.maxConcurrent || '');
  let maxcError: string | undefined;
  // The replacement key lives only in the (uncontrolled) input: read once on submit and cleared at once.
  let keyInput: HTMLInputElement | undefined;

  const text = (fn: (v: string) => void) =>
    on('input', (e: Event & { currentTarget: HTMLInputElement }) => {
      fn(e.currentTarget.value);
      void handle.update();
    });
  const choose = (fn: (v: string) => void) => (v: string) => {
    fn(v);
    void handle.update();
  };

  // Replace (the API key row) opens the form at the key's field
  if (handle.props.focusKey) handle.queueTask(() => keyInput?.focus());

  return () => {
    const { d, uptime, busy, onSave, onCancel } = handle.props;
    return (
      <form
        aria-label="Provider settings"
        autocomplete="off"
        mix={[
          formGrid,
          on('submit', (e) => {
            e.preventDefault();
            // empty is "unlimited" (0); text that is not a whole number is told under the field and nothing is sent. The typed
            // replacement key is read only after this, so it stays in its field when the form is sent back.
            const limit = parseWhole(maxc, 1, MAX_CONCURRENT_ERROR);
            if (!limit.ok) {
              maxcError = limit.error;
              void handle.update();
              const form = e.currentTarget;
              handle.queueTask(() => form.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
              return;
            }
            maxcError = undefined;
            const apiKey = keyInput?.value ?? '';
            if (keyInput) keyInput.value = '';
            onSave({
              id: d.id,
              name,
              baseUrl,
              tier,
              dataClass,
              maxConcurrent: limit.value ?? 0,
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
            <Select id={id} value={tier} options={selectValues(['premium', 'cheap', 'free'])} onChange={choose((v) => (tier = v))} />
          )}
        </Field>
        <Field label="Data class">
          {(id) => (
            <Select
              id={id}
              value={dataClass}
              options={[
                { value: 'private', label: 'private' },
                { value: 'trainable', label: 'trainable', hint: 'public documentation questions only' },
              ]}
              onChange={choose((v) => (dataClass = v))}
            />
          )}
        </Field>
        <Field label="Max concurrent agents (empty: unlimited)" error={maxcError}>
          {(id) => (
            <input
              id={id}
              inputMode="numeric"
              value={maxc}
              {...errorAttrs(id, maxcError)}
              mix={text((v) => ((maxc = v), (maxcError = undefined)))}
            />
          )}
        </Field>
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
          <Button variant="ghost" mix={on('click', () => onCancel())}>
            Cancel
          </Button>
        </div>
      </form>
    );
  };
}

const priceInput = css({ width: '96px' });
const numCell = css({ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' });

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
        <td mix={numCell}>{m.contextLength > 0 ? m.contextLength.toLocaleString() : 'not reported'}</td>
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
            <td mix={numCell}>{priceText(m, 'in')}</td>
            <td mix={numCell}>{priceText(m, 'out')}</td>
          </>
        )}
        <td>
          <PriceSourceBadge m={m} />
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
      <Select label="Quota model" size="sm" value={model} options={[{ value: '', label: 'all models' }, ...selectValues(handle.props.models)]} onChange={(v) => ((model = v), redraw())} />
      <Select label="Quota window" size="sm" value={kind} options={selectOptions(QUOTA_KINDS)} onChange={(v) => ((kind = v), redraw())} />
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
/** What Drydock's own calls to this OpenRouter provider skip: endpoints whose recent uptime is below the threshold. */
function EndpointUptimeSection(handle: Handle<{ v: EndpointUptimeView }>) {
  return () => {
    const { v } = handle.props;
    return (
      <SettingsGroup title="Endpoint uptime" testId="endpoint-uptime">
        <div mix={blockCss}>
          <p mix={muted}>
            {v.minUptime > 0 ? `Drydock's calls skip the endpoints below ${v.minUptime}% recent uptime; when all are below it, the best are kept. ` : 'Off: calls go to OpenRouter without skipping any endpoint. '}
            <LearnMore topic="providers" />
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
      </SettingsGroup>
    );
  };
}

/** The words of an attestation in the provider's row (the form's own sentence is its tooltip). */
const ATTESTATION_WORDS: Record<string, string> = { billing_enabled: 'Billing enabled', lan_attested: 'Plain http on my private network' };

/** The health word: from the last check, also while the provider is disabled. */
function healthWord(status: string): { text: string; tone: 'ok' | 'err' | 'muted' } {
  return status === 'up' ? { text: 'Up', tone: 'ok' } : status === 'down' ? { text: 'Down', tone: 'err' } : { text: 'Not checked', tone: 'muted' };
}

/**
 * One provider in full. `p` is the provider as listed (its state follows the list's reads); the rest comes from provider.detail.
 * `onChanged` reads the list again; `onRemoved` runs once the core has removed it, so the page can choose another row.
 */
export function ProviderPane(handle: Handle<{ p: Provider; onChanged: () => void; onRemoved: () => void }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.providerDetail(handle.props.p.id),
    when: (e) => e.type.startsWith('provider.'),
    key: () => handle.props.p.id,
  });
  const act = action(handle);
  let editing = false;
  let focusKey = false;
  let probe: ProbeResult | undefined;
  let keyInfo: string | undefined;
  const redraw = () => void handle.update();
  const edit = (key: boolean) => {
    editing = true;
    focusKey = key;
    redraw();
  };
  const stopEditing = () => {
    editing = false;
    redraw();
  };

  return () => {
    const { p, onChanged, onRemoved } = handle.props;
    const d = q.data;
    const busy = act.busy !== '';
    const st = providerStatus(p);
    const health = healthWord(p.status);
    const brand = providerBrand(p);
    const attested = Object.entries(p.attestations ?? {});
    return (
      <section mix={panelCss} data-testid="provider" data-name={p.name} data-id={p.id} aria-label={p.name}>
        <DetailHeader
          title={p.name}
          logo={brand}
          icon={KIND_ICON[p.kind] ?? Server}
          status={st}
          sub={providerHeadSub(p)}
          testId="provider-head"
          actions={
            <>
              <Button size="sm" disabled={busy || !d} aria-expanded={editing ? 'true' : 'false'} mix={on('click', () => (editing ? stopEditing() : edit(false)))}>
                Edit
              </Button>
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
              <ConfirmAction
                label="Remove"
                yes="Yes, remove"
                yesName={`Yes, remove ${p.name}`}
                keepName={`Keep ${p.name}`}
                question={p.keyRef ? `Remove ${p.name}? Its API key is deleted from the credential store.` : `Remove ${p.name}?`}
                disabled={busy}
                testId="provider-remove"
                onConfirm={async () => {
                  let ok = false;
                  await act.run('remove', async () => {
                    await s.client.providerDelete(p.id);
                    ok = true;
                  });
                  if (ok) onRemoved();
                }}
              />
            </>
          }
        />
        <ErrorNote error={act.error ?? q.error} what="Provider" />

        {editing && d ? (
          <SettingsGroup title="Edit" testId="provider-edit">
            <div mix={blockCss}>
              <SettingsForm
                key={p.id}
                d={d.provider}
                uptime={d.endpointUptime}
                busy={act.busy}
                focusKey={focusKey}
                onCancel={stopEditing}
                onSave={async (e) => {
                  let ok = false;
                  await act.run('save', async () => {
                    await s.client.providerUpdate(e);
                    ok = true;
                  });
                  if (ok) {
                    stopEditing();
                    q.reload();
                    onChanged();
                  }
                }}
              />
            </div>
          </SettingsGroup>
        ) : (
          <SettingsGroup title="Connection" testId="provider-settings">
            <SettingRow label="Kind">{kindWords(p.kind)}</SettingRow>
            <SettingRow label="Base URL">
              <code>{p.baseUrl}</code>
            </SettingRow>
            <SettingRow label="Data class">
              {p.dataClass === 'trainable' ? (
                <span data-testid="trainable-note">
                  <Badge tone="warn">trainable</Badge> Public documentation questions only; never receives repo content.
                </span>
              ) : (
                <span>
                  <Badge tone="ok">private</Badge> May receive repository content.
                </span>
              )}
            </SettingRow>
            <SettingRow label="Tier">{p.tier}</SettingRow>
            <SettingRow
              label="API key"
              testId="provider-key"
              control={
                <Button size="sm" variant="ghost" disabled={busy || !d} mix={on('click', () => edit(true))}>
                  {p.keyRef ? 'Replace' : 'Add a key'}
                </Button>
              }
            >
              {p.keyRef ? 'Stored in the credential store (never shown)' : 'None'}
            </SettingRow>
            {brand === 'openrouter' ? (
              <SettingRow
                label="Key limits"
                control={
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    mix={on('click', async () => {
                      const r = await act.run('key', () => s.client.providerDetail(p.id, true));
                      if (r) {
                        keyInfo = keyInfoText(r);
                        redraw();
                      }
                    })}
                  >
                    {act.busy === 'key' ? 'Reading...' : 'Read from OpenRouter'}
                  </Button>
                }
              >
                {keyInfo !== undefined ? <span data-testid="key-info">{keyInfo}</span> : <span mix={muted}>Not read yet</span>}
              </SettingRow>
            ) : null}
            <SettingRow label="Attestations" testId="provider-attestations">
              {attested.length === 0
                ? 'None'
                : attested.map(([k, val], i) => (
                    <span key={k} title={ATTESTATIONS.find(([a]) => a === k)?.[1]}>
                      {i > 0 ? ' · ' : ''}
                      {ATTESTATION_WORDS[k] ?? k}: {val ? 'attested' : 'not attested'}
                    </span>
                  ))}
            </SettingRow>
            <SettingRow label="Transport">
              {p.tlsMode}
              {p.lanUnencrypted ? ' (plain http on a private network)' : ''}
            </SettingRow>
            <SettingRow label="Flavor">{p.flavor || 'not detected'}</SettingRow>
            {p.maxConcurrent > 0 ? <SettingRow label="Max concurrent">{p.maxConcurrent}</SettingRow> : null}
          </SettingsGroup>
        )}

        <SettingsGroup title="Health" testId="provider-health">
          <SettingRow
            label="Health check"
            control={
              <Button
                size="sm"
                disabled={busy}
                data-testid="provider-check"
                mix={on('click', () =>
                  act.run('test', async () => {
                    probe = await s.client.providerTest(p.id);
                    onChanged();
                  }),
                )}
              >
                {act.busy === 'test' ? 'Checking...' : 'Check now'}
              </Button>
            }
          >
            <StatusWord text={health.text} tone={health.tone} testId="provider-health-word" />
          </SettingRow>
          <SettingRow label="Last seen">{isZeroTime(p.lastSeen) ? 'never' : formatDateTime(p.lastSeen)}</SettingRow>
          {probe ? (
            <div mix={blockCss}>
              <ProbeView r={probe} />
            </div>
          ) : null}
        </SettingsGroup>

        {!d ? (
          q.error ? null : <Loading />
        ) : (
          <div mix={panelCss} data-testid="provider-detail" role="group" aria-label={`${d.provider.name} details`}>
            <SettingsGroup title="Models" label="Offered models" testId="provider-models" bare>
              {d.models.length === 0 ? (
                <p mix={muted}>None listed yet: Check now reads the provider&apos;s catalog.</p>
              ) : (
                <div mix={tableCard}>
                  <table aria-label="Models and prices">
                    <thead>
                      <tr>
                        <th scope="col">Model</th>
                        <th scope="col">Context</th>
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
                            await act.run('price', () => s.client.providerSetPrice(p.id, m.id, i, o, m.cacheReadPerM, m.cacheWritePerM));
                            q.reload();
                          }}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </SettingsGroup>
            <SettingsGroup title="Quota" testId="provider-quota">
              <div mix={blockCss}>
                {d.quota.length === 0 ? (
                  <p mix={muted}>No limits known: they come from the provider&apos;s headers or API, or from what you set here.</p>
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
                          {f !== undefined ? <Meter value={f} tone={f >= 1 ? 'err' : f >= 0.8 ? 'warn' : 'accent'} label={`${w.kind}: ${w.used} of ${w.limit} used`} /> : null}
                        </li>
                      );
                    })}
                  </ul>
                )}
                <QuotaForm
                  models={d.models.map((m) => m.id)}
                  onAdd={async (model, kind, limit) => {
                    await act.run('quota', () => s.client.providerSetQuota(p.id, model, kind, limit));
                    q.reload();
                  }}
                />
              </div>
            </SettingsGroup>
            {d.endpointUptime ? <EndpointUptimeSection v={d.endpointUptime} /> : null}
          </div>
        )}
      </section>
    );
  };
}

