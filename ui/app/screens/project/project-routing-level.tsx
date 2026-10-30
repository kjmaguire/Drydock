// The project's routing level (decision P118, docs/model-routing.md s3): the level the model router decides this project's
// agent turns at. A project follows the workspace's level (chosen on the Routing page) until it chooses its own here; each
// decision's "Why this model?" names the level and where it came from. Saved as soon as it is chosen.

import type { Handle } from 'remix/component';
import type { RouteLevel } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { action, query, services } from '../../state/services.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Select } from '../../ui/select.tsx';
import { levelSentence, projectLevelOptions } from '../routing/routing.ts';
import { SettingRow, SettingsGroup, errorPad, noteRow } from './settings-kit.tsx';

export function RoutingLevelPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const q = query(handle, {
    load: (): Promise<RouteLevel> => s.client.routeLevelGet(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type === 'routing.level_changed',
  });
  const act = action(handle);
  const choose = async (level: string) => {
    const r = await act.run('set', () => s.client.routeLevelSet(handle.props.projectId, level));
    if (r) q.set(r);
  };
  return () => {
    const rl = q.data;
    const id = `route-level-${handle.id}`;
    return (
      <SettingsGroup
        title="Model routing level"
        label="Model routing level"
        testId="routing-level-panel"
        description="How the model router weighs cost against quality for this project's agent turns, for the agents that have models to choose between."
      >
        {act.error || q.error ? (
          <div mix={errorPad}>
            <ErrorNote error={act.error ?? q.error} what="Not changed" />
          </div>
        ) : null}
        {rl ? (
          <SettingRow label="Level" for={id} description="Follow the workspace, or choose this project's own.">
            <Select id={id} label="Level" value={rl.project} disabled={!!act.busy} options={projectLevelOptions(rl.workspace, rl.project)} onChange={(v) => void choose(v)} />
          </SettingRow>
        ) : null}
        {rl ? (
          <p mix={noteRow} data-testid="routing-level-now" data-source={rl.source}>
            {levelSentence(rl)} Each decision says so under "Why this model?". The workspace's level is on the{' '}
            <a href={routes.routing.href()}>Routing page</a>.
          </p>
        ) : null}
      </SettingsGroup>
    );
  };
}
