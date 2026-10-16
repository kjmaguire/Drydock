// Display parts of the Providers screen: health, the price source, the model catalog and a probe result.

import { css, type Handle } from 'remix/component';
import type { ProbeResult, ProviderModel } from '../../api/types.ts';
import { Badge, muted } from '../../ui/data.tsx';
import { tableCard } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { healthTone, PRICE_HINT, priceKnown, priceText, priceTone, probeDetail, probeLabel } from './providers.ts';

const dotCss = css({ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', flex: 'none' });

/** Health from the last probe: a dot plus the word (never color alone). */
export function HealthBadge(handle: Handle<{ status: string }>) {
  return () => {
    const word = handle.props.status || 'unknown';
    const tn = healthTone(word);
    return (
      <Badge tone={tn} title="Health from the last probe">
        <span mix={dotCss} aria-hidden="true" style={{ background: tone[tn].fg }} />
        {word}
      </Badge>
    );
  };
}

export function PriceSourceBadge(handle: Handle<{ m: ProviderModel }>) {
  return () => {
    const m = handle.props.m;
    if (!priceKnown(m)) return <Badge title="The provider did not report a price and none was entered">no price</Badge>;
    return (
      <Badge tone={priceTone(m.priceSource)} title={PRICE_HINT[m.priceSource] ?? 'Source of the price'}>
        {m.priceSource || 'unknown'}
      </Badge>
    );
  };
}

const numCell = css({ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' });

export function ModelTable(handle: Handle<{ models: ProviderModel[] }>) {
  return () => {
    const models = handle.props.models;
    if (models.length === 0) return <p mix={muted}>No models in the catalog. Run Test to fetch the model list.</p>;
    return (
      <div mix={tableCard}>
        <table aria-label="Model catalog">
          <thead>
            <tr>
              <th scope="col">Model</th>
              <th scope="col">Context</th>
              <th scope="col">Tools</th>
              <th scope="col">In / M</th>
              <th scope="col">Out / M</th>
              <th scope="col">Price source</th>
              <th scope="col">Probe</th>
            </tr>
          </thead>
          <tbody>
            {models.map((m) => (
              <tr key={m.id} data-testid="model">
                <td>
                  <code>{m.id}</code>
                </td>
                <td mix={numCell}>{m.contextLength > 0 ? m.contextLength.toLocaleString() : 'not reported'}</td>
                <td>{m.supportsTools ? 'yes' : 'not verified'}</td>
                <td mix={numCell}>{priceText(m, 'in')}</td>
                <td mix={numCell}>{priceText(m, 'out')}</td>
                <td>
                  <PriceSourceBadge m={m} />
                </td>
                <td>{m.probeStatus || 'not probed'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  };
}

const probeCss = css({
  display: 'grid',
  gap: '6px',
  padding: `${t.s3} ${t.s4}`,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgRaised,
  fontSize: t.textMd,
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& .err': { color: t.danger, overflowWrap: 'anywhere' },
  '& .warn': { color: t.warning, overflowWrap: 'anywhere' },
});

export function ProbeView(handle: Handle<{ r: ProbeResult }>) {
  return () => {
    const r = handle.props.r;
    return (
      <div mix={probeCss} data-testid="probe" data-ok={String(r.OK)}>
        <p class="head">
          <Badge tone={r.OK ? 'ok' : 'err'}>{probeLabel(r)}</Badge>
          <span mix={muted}>{probeDetail(r)}</span>
        </p>
        {r.Error ? <p class="err">{r.Error}</p> : null}
        {(r.Warnings ?? []).map((w, i) => (
          <p key={i} class="warn">
            {w}
          </p>
        ))}
      </div>
    );
  };
}
