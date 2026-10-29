// The workspace's routing level (decision P118, docs/model-routing.md s3): the level the model router decides agent turns at in
// every project that does not choose its own (a project chooses on its settings page). Saved as soon as it is chosen.

import { css, type Handle } from 'remix/component';
import type { RouteLevel } from '../../api/extTypes.ts';
import { action, query, services } from '../../state/services.tsx';
import { faint } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { card, Section } from '../../ui/layout.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { LEVEL_INTENT, LEVELS, levelWords } from './routing.ts';

const bodyCss = css({ display: 'grid', gap: t.s3, maxWidth: '520px' });

export function WorkspaceLevelPanel(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: (): Promise<RouteLevel> => s.client.routeLevelGet(''), when: (e) => e.type === 'routing.level_changed' });
  const act = action(handle);
  const choose = async (level: string) => {
    const r = await act.run('set', () => s.client.routeLevelSet('', level));
    if (r) q.set(r);
  };
  return () => {
    const rl = q.data;
    const levels = rl && !LEVELS.includes(rl.workspace) ? [...LEVELS, rl.workspace] : LEVELS;
    return (
      <Section
        title="Level"
        label="Routing level"
        testId="workspace-level"
        description="How the router weighs cost against quality for agent turns. Every project runs at this level unless it chooses its own in its settings."
      >
        <ErrorNote error={act.error ?? q.error} what="Level not changed" />
        {!rl ? (
          q.error ? null : <Loading />
        ) : (
          <div mix={[card, bodyCss]}>
            <Field label="Workspace level">
              {(id) => (
                <Select
                  id={id}
                  value={rl.workspace}
                  disabled={!!act.busy}
                  options={levels.map((l) => ({ value: l, label: levelWords(l), hint: LEVEL_INTENT[l] }))}
                  onChange={(v) => void choose(v)}
                />
              )}
            </Field>
            <p mix={faint} data-testid="workspace-level-note">
              {levelWords(rl.workspace)}: {LEVEL_INTENT[rl.workspace] ?? 'as the stored rules say'}. "Why this model?" names the level of each decision and whether
              it was the project's own.
            </p>
          </div>
        )}
      </Section>
    );
  };
}
