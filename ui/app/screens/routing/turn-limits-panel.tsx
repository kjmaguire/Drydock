// The Routing page's tiebreak and turn limits (decision P112): the LLM tiebreak (off until a person turns it on, with its own
// daily budget and a timeout), the wall-time ceiling of a turn per level and per agent (none until set), and the models a
// provider refused for want of data retention, each with "Try again". Nothing here changes until the person saves.

import { css, on, type Handle } from 'remix/component';
import type { RouteSettings } from '../../api/extTypes.ts';
import { formatUsd, plural } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox, errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { card, Section } from '../../ui/layout.tsx';
import { Select, selectOptions } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ceilingWords, LEVELS, minutesOf, parseMinutes } from './routing.ts';

const LEVEL_WORDS: Record<string, string> = { cheapest: 'Cheapest', fast: 'Fast', balanced: 'Balanced', quality: 'Quality', maximum_quality: 'Maximum quality' };

const gridCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))',
  gap: t.s3,
  alignItems: 'end',
  '& .wide': { gridColumn: '1 / -1' },
});
const blockCss = css({ display: 'grid', gap: t.s3 });
const subHead = css({ fontSize: t.textMd, fontWeight: '600', margin: '0' });
const rowsCss = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  display: 'grid',
  gap: t.s2,
  '& li': { display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' },
  '& .who': { flex: '1', minWidth: '0', overflowWrap: 'anywhere' },
  '& input': { width: '90px' },
});

interface Draft {
  enabled: boolean;
  budget: string;
  timeout: string;
  levels: Record<string, string>;
  agents: Record<string, string>;
  adding: string;
}

const draftOf = (s: RouteSettings): Draft => ({
  enabled: s.tiebreak.enabled,
  budget: String(s.tiebreak.budgetUsd),
  timeout: String(Math.round(s.tiebreak.timeoutMs / 1000)),
  levels: Object.fromEntries(LEVELS.map((l) => [l, minutesOf(s.ceilings.levels[l])])),
  agents: Object.fromEntries(Object.entries(s.ceilings.agents).map(([id, sec]) => [id, minutesOf(sec)])),
  adding: '',
});

export function TurnLimitsPanel(handle: Handle) {
  const s = services(handle);
  const settings = query(handle, { load: () => s.client.routeSettingsGet(), when: (e) => e.type === 'routing.settings_changed' || e.type === 'routing.level_changed' });
  const aside = query(handle, { load: () => s.client.routeRetentionList(), when: (e) => e.type.startsWith('provider.retention') });
  const tie = action(handle);
  const lim = action(handle);
  const clear = action(handle);
  let draft: Draft | undefined;
  let budgetError: string | undefined;
  let timeoutError: string | undefined;
  let minuteErrors: Record<string, string> = {};
  const redraw = () => void handle.update();
  const input = (fn: (v: string) => void) => on('input', (e: Event & { currentTarget: HTMLInputElement }) => {
    fn(e.currentTarget.value);
    redraw();
  });

  const saved = (r: RouteSettings | undefined) => {
    if (!r) return;
    settings.set(r);
    draft = draftOf(r);
    redraw();
  };

  const saveTiebreak = async () => {
    const d = draft!;
    const budget = Number(d.budget);
    const timeout = Number(d.timeout);
    budgetError = d.budget.trim() === '' || !Number.isFinite(budget) || budget < 0 || budget > 10 ? 'A dollar amount from 0 to 10.' : undefined;
    timeoutError = !/^\d+$/.test(d.timeout.trim()) || timeout < 1 || timeout > 60 ? 'A whole number of seconds from 1 to 60.' : undefined;
    redraw();
    if (budgetError || timeoutError) return;
    saved(await tie.run('save', () => s.client.routeSettingsSet({ tiebreak: { enabled: d.enabled, budgetUsd: budget, timeoutMs: timeout * 1000 } })));
  };

  const saveLimits = async () => {
    const d = draft!;
    const levels: Record<string, number> = {};
    const agents: Record<string, number> = {};
    minuteErrors = {};
    for (const [key, text, into] of [...Object.entries(d.levels).map(([k, v]) => [`level:${k}`, v, levels] as const), ...Object.entries(d.agents).map(([k, v]) => [`agent:${k}`, v, agents] as const)]) {
      const m = parseMinutes(text);
      if (!m.ok) minuteErrors[key] = m.error;
      else if (m.seconds > 0) into[key.slice(key.indexOf(':') + 1)] = m.seconds;
    }
    redraw();
    if (Object.keys(minuteErrors).length > 0) return;
    saved(await lim.run('save', () => s.client.routeSettingsSet({ ceilings: { levels, agents } })));
  };

  return () => {
    const cur = settings.data;
    if (cur && !draft) draft = draftOf(cur);
    const blocks = aside.data ?? [];
    return (
      <Section
        title="Tiebreak and turn limits"
        label="Tiebreak and turn limits"
        testId="route-limits"
        description="What happens when the rules leave models tied, and how long one turn may run. Both are off until you set them."
      >
        <ErrorNote error={settings.error} what="Could not read the routing settings" />
        {!cur || !draft ? (
          settings.error ? null : <Loading />
        ) : (
          <div mix={[card, blockCss]}>
            <h3 mix={subHead}>Tiebreak</h3>
            <ErrorNote error={tie.error} what="Tiebreak not saved" />
            <Checkbox
              label="Ask the cheap model when the rules leave a tie"
              description={`Models within ${Math.round((cur.tiebreak.margin ?? 0.1) * 100)}% of each other: one short call chooses, told the kind of turn and the models' facts, never the work. If it fails, the rules' choice stands.`}
              checked={draft.enabled}
              onChange={(v) => {
                draft!.enabled = v;
                redraw();
              }}
            />
            <div mix={gridCss}>
              <Field label="Daily budget (USD)" error={budgetError}>
                {(id) => <input id={id} inputMode="decimal" value={draft!.budget} {...errorAttrs(id, budgetError)} mix={input((v) => ((draft!.budget = v), (budgetError = undefined)))} />}
              </Field>
              <Field label="Timeout (seconds)" error={timeoutError}>
                {(id) => <input id={id} inputMode="numeric" value={draft!.timeout} {...errorAttrs(id, timeoutError)} mix={input((v) => ((draft!.timeout = v), (timeoutError = undefined)))} />}
              </Field>
              <p class="wide" mix={faint} data-testid="tiebreak-model">
                {cur.tiebreak.model ? (
                  <>
                    Calls <code>{cur.tiebreak.model}</code>. Today: {formatUsd(cur.tiebreak.spentTodayUsd)} over {plural(cur.tiebreak.callsToday, 'call')}.
                  </>
                ) : (
                  cur.tiebreak.modelNote
                )}
              </p>
            </div>
            <div mix={formActions}>
              <Button variant="primary" disabled={tie.busy !== ''} mix={on('click', () => void saveTiebreak())}>
                {tie.busy ? 'Saving...' : 'Save tiebreak'}
              </Button>
            </div>

            <h3 mix={subHead}>Wall-time limit of a turn</h3>
            <p mix={muted}>
              A turn that runs longer is stopped and waits for you; one that did nothing yet moves along its fallback chain. Empty is no limit.
            </p>
            <p mix={faint}>A turn's limit is its level's: projects without a level of their own run at {LEVEL_WORDS[cur.level] ?? cur.level}.</p>
            <ErrorNote error={lim.error} what="Limits not saved" />
            <div mix={gridCss}>
              {LEVELS.map((l) => (
                <Field key={l} label={`${LEVEL_WORDS[l] ?? l} (minutes)`} error={minuteErrors[`level:${l}`]}>
                  {(id) => (
                    <input
                      id={id}
                      inputMode="numeric"
                      placeholder="no limit"
                      value={draft!.levels[l] ?? ''}
                      data-level={l}
                      {...errorAttrs(id, minuteErrors[`level:${l}`])}
                      mix={input((v) => {
                        draft!.levels[l] = v;
                        delete minuteErrors[`level:${l}`];
                      })}
                    />
                  )}
                </Field>
              ))}
            </div>
            <ul mix={rowsCss} aria-label="Limits for one agent">
              {Object.keys(draft.agents).map((id) => {
                const a = cur.agents.find((x) => x.id === id);
                const err = minuteErrors[`agent:${id}`];
                return (
                  <li key={id} data-testid="agent-limit" data-agent={id}>
                    <span class="who">
                      {a ? a.name : id} {a?.project ? <span mix={faint}>{a.project}</span> : null}
                    </span>
                    <input
                      aria-label={`Minutes for ${a ? a.name : id}`}
                      inputMode="numeric"
                      value={draft!.agents[id]}
                      {...(err ? { 'aria-invalid': 'true' as const } : {})}
                      mix={input((v) => {
                        draft!.agents[id] = v;
                        delete minuteErrors[`agent:${id}`];
                      })}
                    />
                    <span mix={faint}>minutes{err ? `: ${err}` : ''}</span>
                    <Button
                      size="sm"
                      variant="ghost"
                      mix={on('click', () => {
                        delete draft!.agents[id];
                        redraw();
                      })}
                    >
                      Remove
                    </Button>
                  </li>
                );
              })}
            </ul>
            {cur.agents.some((a) => !(a.id in draft!.agents)) ? (
              <div mix={gridCss}>
                <Field label="A limit for one agent (it wins over the level's)">
                  {(id) => (
                    <Select
                      id={id}
                      value={draft!.adding}
                      placeholder="Choose an agent..."
                      options={selectOptions(cur.agents.filter((a) => !(a.id in draft!.agents)).map((a) => [a.id, a.project ? `${a.name} (${a.project})` : a.name]))}
                      onChange={(v) => {
                        draft!.adding = v;
                        redraw();
                      }}
                    />
                  )}
                </Field>
                <div>
                  <Button
                    disabled={!draft.adding}
                    mix={on('click', () => {
                      draft!.agents[draft!.adding] = '';
                      draft!.adding = '';
                      redraw();
                    })}
                  >
                    Add
                  </Button>
                </div>
              </div>
            ) : null}
            <div mix={formActions}>
              <Button variant="primary" disabled={lim.busy !== ''} mix={on('click', () => void saveLimits())}>
                {lim.busy ? 'Saving...' : 'Save limits'}
              </Button>
            </div>
            <p mix={faint} role="status" data-testid="limits-now">
              {Object.keys(cur.ceilings.levels).length + Object.keys(cur.ceilings.agents).length === 0
                ? 'No limit is set.'
                : `Set: ${[
                    ...Object.entries(cur.ceilings.levels).map(([l, sec]) => `${LEVEL_WORDS[l] ?? l} ${ceilingWords(sec)}`),
                    ...Object.entries(cur.ceilings.agents).map(([id, sec]) => `${cur.agents.find((a) => a.id === id)?.name ?? id} ${ceilingWords(sec)}`),
                  ].join(', ')}.`}
            </p>
          </div>
        )}
        {blocks.length > 0 ? (
          <div mix={[card, blockCss]} data-testid="retention-blocks">
            <h3 mix={subHead}>Models set aside</h3>
            <p mix={muted}>
              The provider refused these models: your organization has no data retention enabled. The router leaves them out until you try again.
            </p>
            <p mix={faint}>Enable retention in the provider&apos;s console (the model then keeps prompts and outputs for 30 days), or use another model.</p>
            <ErrorNote error={clear.error} what="Could not try again" />
            <ul mix={rowsCss} aria-label="Models set aside">
              {blocks.map((b) => (
                <li key={`${b.providerId}/${b.model}`} data-testid="retention-block">
                  <span class="who">
                    <code>
                      {b.providerId}/{b.model}
                    </code>{' '}
                    <span mix={faint}>{b.source === 'agent' ? "an agent's turn" : "one of Drydock's own calls"}</span>
                  </span>
                  <Button
                    size="sm"
                    disabled={clear.busy !== ''}
                    mix={on('click', async () => {
                      await clear.run(`${b.providerId}/${b.model}`, () => s.client.routeRetentionClear(b.providerId, b.model));
                      if (!clear.error) aside.set(blocks.filter((x) => x !== b));
                    })}
                  >
                    Try again
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </Section>
    );
  };
}
