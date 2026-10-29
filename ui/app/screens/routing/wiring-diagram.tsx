// The routing wiring diagram (decision P108): one card per level of agent work and per kind of turn, each saying the model the
// router would choose now, wired to the router. The answers are dry runs, all in one call (route.previews): nothing is recorded
// and no quota is taken (a tie the cheap model would break is said, not asked: P112). They are read again when a rule, a
// provider, the cheap model or the routing settings change. Layout and words: wiring.ts.

import { css, type Handle } from 'remix/component';
import { Route as RouteIcon } from 'lucide';
import type { RoutePreview } from '../../api/extTypes.ts';
import { query, services } from '../../state/services.tsx';
import { routes } from '../../routes.ts';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Section } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { sharedBlock, WIRE, wireAnswer, wireGroups, wireLayout, wirePath, type WireTone } from './wiring.ts';

const COLOR: Record<WireTone, string> = {
  ok: tone.ok.fg,
  info: tone.info.fg,
  accent: tone.accent.fg,
  warn: tone.warn.fg,
  err: tone.err.fg,
  muted: t.textFaint,
};

const boardCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 300px) minmax(48px, 1fr) minmax(120px, 160px)',
  minWidth: '0',
  '@media (max-width: 640px)': { gridTemplateColumns: 'minmax(0, 1fr) 32px 96px' },
});
const colCss = css({ position: 'relative', minWidth: '0' });
const headCss = css({
  position: 'absolute',
  left: '0',
  right: '0',
  height: `${WIRE.head}px`,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: t.s2,
  fontSize: t.textXs,
  fontWeight: '600',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  color: t.textFaint,
  '& .note': { textTransform: 'none', letterSpacing: '0', fontWeight: '400' },
});
const cardCss = css({
  position: 'absolute',
  left: '0',
  right: '0',
  height: `${WIRE.row}px`,
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `0 ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderLeftWidth: '3px',
  borderRadius: t.rMd,
  background: t.bgSurface,
  minWidth: '0',
  '& .label': { fontWeight: '500', whiteSpace: 'nowrap', flex: 'none' },
  '& .arrow': { color: t.textFaint, flex: 'none' },
  '& .to': { display: 'grid', minWidth: '0', marginLeft: 'auto', textAlign: 'right' },
  '& .model': { fontWeight: '500', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .detail': { fontSize: t.textXs, color: t.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '&[data-blocked="true"] .model': { color: t.danger },
});
const wiresCss = css({ position: 'absolute', inset: '0', width: '100%', height: '100%', overflow: 'visible' });
const nodeCss = css({
  position: 'absolute',
  left: '0',
  right: '0',
  display: 'grid',
  justifyItems: 'center',
  gap: '6px',
  padding: t.s3,
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  textAlign: 'center',
  transform: 'translateY(-50%)',
  '& .mark': { display: 'grid', placeItems: 'center', width: '36px', height: '36px', borderRadius: '50%', background: t.accentSoft, color: t.accentText },
  '& .name': { fontWeight: '600' },
  '& .sub': { fontSize: t.textXs, color: t.textMuted },
});

export function WiringDiagram(handle: Handle) {
  const s = services(handle);
  const groups = wireGroups();
  const rows = groups.flatMap((g) => g.rows);
  // every card's dry run in one call; one that fails says so on its card and the others still answer
  const answers = query(handle, {
    load: async () => (await s.client.routePreviews(rows.map((r) => r.request))).map((x): RoutePreview | Error => ('error' in x ? new Error(x.error) : x)),
    when: (e) => e.type === 'routing.rule_changed' || e.type === 'routing.settings_changed' || e.type.startsWith('provider.') || e.type.startsWith('route.cheap'),
    debounceMs: 300,
  });
  const layout = wireLayout(groups);

  return () => {
    const got = answers.data;
    const at = (key: string) => layout.rows.find((x) => x.key === key)!;
    const blocked = sharedBlock(got);
    return (
      <Section
        title="Wiring"
        label="Routing wiring"
        testId="route-wiring"
        description="What the router would choose right now for each level of agent work and each kind of turn: a dry run, nothing is recorded and no quota is taken."
      >
        <ErrorNote error={answers.error} what="Wiring" />
        {blocked ? (
          <Notice tone="warn" testId="wire-blocked">
            Every turn is blocked: {blocked}. Add a model provider in <a href={routes.models.href()}>Models</a>.
          </Notice>
        ) : null}
        <div mix={boardCss} style={{ height: `${layout.height}px` }}>
          <div mix={colCss}>
            {groups.map((g) => (
              <div key={g.key} mix={headCss} style={{ top: `${layout.heads.find((h) => h.key === g.key)!.top}px` }} data-testid="wire-group">
                <span>{g.title}</span>
                <span class="note">{g.note}</span>
              </div>
            ))}
            <ul aria-label="What the router chooses" style={{ margin: '0', padding: '0', listStyle: 'none' }}>
              {rows.map((r, i) => {
                const a = wireAnswer(got?.[i]);
                return (
                  <li
                    key={r.key}
                    mix={cardCss}
                    style={{ top: `${at(r.key).top}px`, borderLeftColor: COLOR[r.tone] }}
                    title={a.title || undefined}
                    data-testid="wire-card"
                    data-key={r.key}
                    data-blocked={String(a.blocked)}
                  >
                    <span class="label">{r.label}</span>
                    <span class="arrow" aria-hidden="true">
                      →
                    </span>
                    <span class="to">
                      <span class="model" data-testid="wire-model">
                        {a.model}
                      </span>
                      {a.detail ? <span class="detail">{a.detail}</span> : null}
                    </span>
                  </li>
                );
              })}
            </ul>
          </div>
          <div mix={colCss} aria-hidden="true">
            <svg mix={wiresCss} viewBox={`0 0 100 ${layout.height}`} preserveAspectRatio="none" data-testid="wire-lines">
              {rows.map((r) => (
                <path
                  key={r.key}
                  d={wirePath(at(r.key).mid, layout.node)}
                  fill="none"
                  stroke={COLOR[r.tone]}
                  stroke-width={r.tone === 'muted' ? '1' : '1.75'}
                  stroke-opacity={r.tone === 'muted' ? '0.7' : '1'}
                  vector-effect="non-scaling-stroke"
                  data-key={r.key}
                />
              ))}
            </svg>
          </div>
          <div mix={colCss}>
            <div mix={nodeCss} style={{ top: `${layout.node}px` }} data-testid="wire-node">
              <span class="mark">
                <Icon icon={RouteIcon} size={18} />
              </span>
              <span class="name">Router</span>
              <span class="sub">picks the model for every turn</span>
            </div>
          </div>
        </div>
      </Section>
    );
  };
}
