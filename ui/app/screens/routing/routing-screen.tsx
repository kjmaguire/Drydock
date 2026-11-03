// Model routing (a workspace screen): what the router chooses now, wired (wiring-diagram.tsx, decision P108), the rules the
// router evaluates, a dry-run preview of a decision, the tiebreak and the wall-time limits of a turn (turn-limits-panel.tsx,
// decision P112), and the cheap model selection with its opt-in startup probe (its real state as a switch, effort review F8). The
// rules, the preview and the limits are folded (the P144 pattern). Nothing here is decided without a button press by the human.

import { css, on, type Handle } from 'remix/component';
import { FlaskConical, Route as RouteIcon, TriangleAlert } from 'lucide';
import type { CheapSelection, RoutePreview, RouteSettings } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { action, query, retryFor, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Field, Switch } from '../../ui/form.tsx';
import { Select, selectOptions } from '../../ui/select.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { money } from '../shared/explain.tsx';
import { WorkspaceLevelPanel } from './level-panel.tsx';
import { cheapProviders, LEVELS, runKindOptions } from './routing.ts';
import { RulesPanel } from './rules-panel.tsx';
import { TurnLimitsPanel } from './turn-limits-panel.tsx';
import { WiringDiagram } from './wiring-diagram.tsx';

const previewForm = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))',
  gap: t.s3,
  alignItems: 'end',
});
const resultCss = css({
  display: 'grid',
  gap: t.s3,
  '& ul': { margin: '0', paddingLeft: '20px', color: t.textMuted, fontSize: t.textSm },
});

function select(label: string, value: () => string, set: (v: string) => void, options: [string, string][], redraw: () => void) {
  return (
    <Field label={label}>
      {(id) => (
        <Select
          id={id}
          value={value()}
          options={selectOptions(options)}
          onChange={(v) => {
            set(v);
            redraw();
          }}
        />
      )}
    </Field>
  );
}

export function PreviewPanel(handle: Handle) {
  const s = services(handle);
  const act = action(handle);
  let runKind = 'agent_task';
  let level = 'balanced';
  let role = 'backend';
  let ceiling = 'private';
  let result: RoutePreview | undefined;
  const redraw = () => void handle.update();

  const preview = async () => {
    result = await act.run('preview', () => s.client.routePreview({ RunKind: runKind, Level: level, Role: role, ContextCeiling: ceiling }));
    redraw();
  };

  return () => (
    <Section
      title="Preview a decision"
      label="Preview a routing decision"
      description="A dry run with the same rules as a real decision. Nothing is recorded and no quota is taken."
    >
      <div mix={[card, resultCss]}>
        <div mix={previewForm}>
          {select('Run kind', () => runKind, (v) => (runKind = v), runKindOptions(), redraw)}
          {select('Level', () => level, (v) => (level = v), LEVELS.map((k) => [k, k]), redraw)}
          <Field label="Role">
            {(id) => (
              <input
                id={id}
                value={role}
                mix={on('input', (e) => {
                  role = e.currentTarget.value;
                  redraw();
                })}
              />
            )}
          </Field>
          {select(
            'Context',
            () => ceiling,
            (v) => (ceiling = v),
            [
              ['private', 'private (repository content)'],
              ['public_docs', 'public documentation only'],
            ],
            redraw,
          )}
          <div>
            <Button variant="primary" icon={FlaskConical} disabled={act.busy !== ''} mix={on('click', () => void preview())}>
              Preview
            </Button>
          </div>
        </div>
        <ErrorNote error={act.error} what="Preview failed" />
        {result ? (
          <div mix={resultCss} data-testid="route-preview" data-blocked={String(result.blocked)}>
            {result.blocked ? (
              <Notice tone="err" role="status">
                Blocked: {result.reason}
              </Notice>
            ) : (
              <>
                <p>
                  Would use{' '}
                  <code>
                    {result.decision?.Provider}/{result.decision?.Model || 'default'}
                  </code>{' '}
                  (effort {result.decision?.Effort || 'not set'}), estimated cost {money(result.decision?.EstCostUSD)}.
                </p>
                {result.explain ? <Markdown source={result.explain} /> : null}
              </>
            )}
            {(result.alternatives ?? []).length > 0 ? (
              <ul aria-label="Alternatives not chosen">
                {(result.alternatives ?? []).map((a, i) => (
                  <li key={i}>
                    {a.Provider}/{a.Model} rejected: {a.Rejected} {a.Detail}
                  </li>
                ))}
              </ul>
            ) : null}
            <p mix={faint}>Recorded: no.</p>
          </div>
        ) : null}
      </div>
    </Section>
  );
}

function CheapStored(handle: Handle<{ providerId: string; fresh?: CheapSelection }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.routeCheapGet(handle.props.providerId),
    key: () => [handle.props.providerId, handle.props.fresh?.message],
  });
  return () => {
    const sel = handle.props.fresh ?? q.data?.selection ?? undefined;
    if (!sel) return <span mix={muted}>no selection yet</span>;
    return (
      <span data-testid="cheap-selection">
        default <code>{sel.default || 'none passed'}</code>, fallback <code>{sel.fallback || 'none'}</code> <span mix={muted}>{sel.message}</span>
      </span>
    );
  };
}

const cheapList = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > li': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s3,
    flexWrap: 'wrap',
    padding: `10px ${t.s4}`,
    borderTop: `1px solid ${t.border}`,
    fontSize: t.textMd,
  },
  '& > li:first-child': { borderTop: '0' },
  '& .sel': { flex: '1', minWidth: '0', overflowWrap: 'anywhere' },
});
const warnCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  borderRadius: t.rLg,
  background: t.warningSoft,
  border: `1px solid color-mix(in srgb, ${t.warning} 35%, transparent)`,
  '& .row': { display: 'flex', gap: t.s2, flexWrap: 'wrap' },
  '& .msg': { display: 'flex', gap: t.s2, alignItems: 'flex-start' },
  '& .msg svg': { color: t.warning, marginTop: '2px' },
});

/** Who set the startup probe, in words: "human:kyle" reads "kyle". */
export function setterName(by: string | undefined): string {
  return (by ?? '').replace(/^human:/, '').trim();
}

export function CheapModelPanel(handle: Handle) {
  const s = services(handle);
  const providers = query(handle, { load: () => s.client.providerList(), when: (e) => e.type.startsWith('provider.') });
  // the startup probe's real state, from the core (effort review F8)
  const settings = query(handle, { load: () => s.client.routeSettingsGet(), when: (e) => e.type === 'routing.settings_changed' });
  const act = action(handle);
  const startup = action(handle);
  let out: Record<string, CheapSelection> = {};
  let confirm = false; // turning it on: the cost warning is showing
  const redraw = () => void handle.update();

  const setStartup = async (on_: boolean) => {
    const r = await startup.run(on_ ? 'on' : 'off', () => s.client.routeProbeStartup(on_));
    if (r) {
      confirm = false;
      const cur = settings.data;
      if (cur) settings.set({ ...cur, probeStartup: r.enabled, probeStartupBy: r.by });
      void settings.reload();
    }
    redraw();
  };

  return () => {
    const cheap = cheapProviders(providers.data ?? []);
    const st = settings.data;
    const probeOn = !!st?.probeStartup;
    const who = setterName(st?.probeStartupBy);
    return (
      <Section
        title="Cheap model selection"
        label="Cheap model selection"
        description="Tests the cheapest few models of each cheap, private provider with a small tool-call check. It can cost a little."
      >
        <ErrorNote error={providers.error ?? act.error} retry={retryFor(providers, act)} what="Selection failed" />
        {providers.data && cheap.length === 0 ? (
          <p mix={[card, muted]}>
            No enabled cheap private provider. Add one under <a href={routes.models.href({ key: 'new' })}>Models</a>.
          </p>
        ) : null}
        {cheap.length > 0 ? (
          <ul mix={cheapList} aria-label="Cheap private providers">
            {cheap.map((p) => (
              <li key={p.id} data-testid="cheap-provider">
                <strong>{p.name}</strong>
                {p.enabled ? null : <Badge>disabled</Badge>}
                <span class="sel">
                  <CheapStored providerId={p.id} fresh={out[p.id]} />
                </span>
                <Button
                  size="sm"
                  disabled={act.busy !== ''}
                  mix={on('click', async () => {
                    const r = await act.run(p.id, () => s.client.routeCheapSelect(p.id, false));
                    if (r) {
                      out = { ...out, [p.id]: r };
                      redraw();
                    }
                  })}
                >
                  {act.busy === p.id ? 'Probing...' : 'Probe and select now'}
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        <ErrorNote error={startup.error ?? settings.error} retry={retryFor(startup, settings)} what="Startup probe" />
        <div data-testid="startup-probe" data-on={st ? String(probeOn) : undefined}>
          <Switch
            label="Probe at startup"
            checked={probeOn || confirm}
            disabled={!st || startup.busy !== ''}
            onChange={(v) => {
              if (v) {
                confirm = true; // the cost is said first; nothing is changed until Turn on
                redraw();
              } else if (confirm) {
                confirm = false;
                redraw();
              } else void setStartup(false);
            }}
          />
          <p role="status" mix={faint} data-testid="startup-probe-state">
            {!st ? 'Reading...' : probeOn ? `On${who ? `, turned on by ${who}` : ''}.` : who ? `Off, turned off by ${who}.` : 'Off (the default).'}
          </p>
        </div>
        {confirm ? (
          <div mix={warnCss} data-testid="startup-probe-confirm">
            <p class="msg">
              <Icon icon={TriangleAlert} size={15} />
              <span>
                The startup probe makes a few test requests every time Drydock starts. That can cost a little. It is off unless you turn it on.
              </span>
            </p>
            <div class="row">
              <Button variant="primary" disabled={startup.busy !== ''} mix={on('click', () => void setStartup(true))}>
                Turn on
              </Button>
              <Button
                variant="ghost"
                mix={on('click', () => {
                  confirm = false;
                  redraw();
                })}
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : null}
      </Section>
    );
  };
}

/**
 * Whether the tiebreak or a turn limit is set: their fold then opens by itself, so a setting in force is never out of sight
 * (the P144 pattern, effort review F8).
 */
export function limitsInUse(st: RouteSettings | undefined): boolean {
  if (!st) return false;
  const any = (m: Record<string, number> | undefined) => Object.values(m ?? {}).some((v) => v > 0);
  return !!st.tiebreak?.enabled || any(st.ceilings?.levels) || any(st.ceilings?.agents);
}

export function RoutingScreen(handle: Handle) {
  const s = services(handle);
  const settings = query(handle, { load: () => s.client.routeSettingsGet(), when: (e) => e.type === 'routing.settings_changed' });
  return () => (
    <Page title="Routing" icon={RouteIcon} subtitle="Which model answers each turn" guide="routing-rules">
      <WiringDiagram />
      <WorkspaceLevelPanel />
      <CheapModelPanel />
      {/* the power controls, one click away: each fold keeps what the person opened or closed */}
      <Disclosure summary="Rules" testId="routing-rules-fold" wide>
        <RulesPanel />
      </Disclosure>
      <Disclosure summary="Preview a decision" testId="routing-preview-fold" wide>
        <PreviewPanel />
      </Disclosure>
      <Disclosure summary="Tiebreak and turn limits" open={limitsInUse(settings.data)} testId="routing-limits-fold" wide>
        <TurnLimitsPanel />
      </Disclosure>
    </Page>
  );
}
