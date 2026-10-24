// Display parts of a provider's detail: the price source and a probe result.

import { css, type Handle } from 'remix/component';
import type { ProbeResult, ProviderModel } from '../../api/types.ts';
import { Badge, muted } from '../../ui/data.tsx';
import { t } from '../../ui/tokens.ts';
import { PRICE_HINT, priceKnown, priceTone, probeDetail, probeLabel } from './providers.ts';

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
