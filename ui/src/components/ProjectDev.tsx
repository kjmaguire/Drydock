import { useEffect, useState } from 'react';
import type { CheckSettings, DevSettings } from '../api/extTypes';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { ErrorNote } from './common';

const argv = (s: string): string[] => s.split('\n').map((l) => l.trim()).filter(Boolean);

/** Extra verification checks per project (checks.settings.get/set). Only the human changes them. */
export function CheckSettingsPanel({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.checksSettingsGet(projectId), [projectId], (e) => e.type.startsWith('project.'));
  const act = useAction();
  const [s, setS] = useState<CheckSettings | undefined>();
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (q.data) setS(q.data);
  }, [q.data]);
  if (!s) return <ErrorNote error={q.error} what="Could not load check settings" />;
  const set = (patch: Partial<CheckSettings>) => { setS({ ...s, ...patch }); setSaved(false); };
  return (
    <section aria-label="Verification checks">
      <h2>Verification checks</h2>
      <p className="muted">Build, test and lint always run. These add checks to every task. A check whose tool is missing is reported as skipped, never as passed.</p>
      <ErrorNote error={act.error} what="Not saved" />
      <label className="inline">
        <input type="checkbox" checked={s.secrets} onChange={(e) => set({ secrets: e.target.checked })} /> Secrets scan of the diff
      </label>
      <br />
      <label className="inline">
        <input type="checkbox" checked={s.deps} onChange={(e) => set({ deps: e.target.checked })} /> Dependency vulnerability scan
      </label>
      <br />
      <label className="inline">
        <input type="checkbox" checked={s.depsOffline} onChange={(e) => set({ depsOffline: e.target.checked })} disabled={!s.deps} /> Offline only (no database download)
      </label>
      <div className="form-actions">
        <button className="primary" disabled={act.busy !== ''} onClick={async () => { const r = await act.run('save', () => client.checksSettingsSet(projectId, s)); if (r) { setS(r); setSaved(true); } }}>
          Save check settings
        </button>
        {saved ? <span role="status">Saved.</span> : null}
      </div>
    </section>
  );
}

/** Development flow settings per project: parallelism, the scheduler, room replies, e2e and preview (projectdev.get/set). */
export function DevSettingsPanel({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.projectdevGet(projectId), [projectId], () => false);
  const act = useAction();
  const [s, setS] = useState<DevSettings | undefined>();
  const [e2eCmd, setE2eCmd] = useState('');
  const [prevCmd, setPrevCmd] = useState('');
  const [saved, setSaved] = useState(false);
  const load = (d: DevSettings) => {
    setS(d);
    setE2eCmd((d.e2e.command ?? []).join('\n'));
    setPrevCmd((d.preview.command ?? []).join('\n'));
  };
  useEffect(() => {
    if (q.data) load(q.data);
  }, [q.data]);
  if (!s) return <ErrorNote error={q.error} what="Could not load development settings" />;
  const patch = (p: Partial<DevSettings>) => { setS({ ...s, ...p }); setSaved(false); };
  const save = async () => {
    const body: DevSettings = {
      ...s,
      e2e: { ...s.e2e, command: argv(e2eCmd), timeoutSec: s.e2e.timeoutSec ?? 0 },
      preview: { ...s.preview, command: argv(prevCmd), timeoutSec: s.preview.timeoutSec ?? 0 },
    };
    const r = await act.run('save', () => client.projectdevSet(projectId, body));
    if (r) { load(r); setSaved(true); }
  };
  return (
    <section aria-label="Development flow">
      <h2>Development flow</h2>
      <ErrorNote error={act.error} what="Not saved" />
      <div className="form">
        <label>
          Maximum parallel agent turns
          <input aria-label="Maximum parallel agent turns" inputMode="numeric" value={String(s.maxParallel)} onChange={(e) => patch({ maxParallel: numOrUndef(e.target.value) ?? 0 })} />
        </label>
        {s.maxParallel > 5 ? (
          <p className="muted" data-testid="parallel-note">
            Anthropic suggests starting with 3 to 5 agents working at once: token cost grows with every active agent, coordination gets harder, and beyond that more agents rarely finish the work faster.
          </p>
        ) : null}
        <label className="inline">
          <input type="checkbox" checked={s.autoSchedule} onChange={(e) => patch({ autoSchedule: e.target.checked })} /> Start ready tasks automatically (scheduler)
        </label>
        <label className="inline">
          <input type="checkbox" checked={s.collisionGuard !== false} onChange={(e) => patch({ collisionGuard: e.target.checked })} /> Make a task wait while an earlier task is changing the same code
        </label>
        <label className="inline">
          <input type="checkbox" checked={s.respond} onChange={(e) => patch({ respond: e.target.checked })} /> Agents answer in channels and direct messages
        </label>
        <fieldset>
          <legend>End-to-end check</legend>
          <label className="inline">
            <input type="checkbox" checked={s.e2e.enabled} onChange={(e) => patch({ e2e: { ...s.e2e, enabled: e.target.checked } })} /> Run e2e tests in the task worktree
          </label>
          <label>
            Command (one argument per line; empty detects Taskfile, package.json script or Playwright config)
            <textarea value={e2eCmd} onChange={(e) => { setE2eCmd(e.target.value); setSaved(false); }} rows={2} spellCheck={false} />
          </label>
        </fieldset>
        <fieldset>
          <legend>Preview</legend>
          <label className="inline">
            <input type="checkbox" checked={s.preview.enabled} onChange={(e) => patch({ preview: { ...s.preview, enabled: e.target.checked } })} /> Start a preview of the task before approval
          </label>
          <label>
            Command (one argument per line)
            <textarea value={prevCmd} onChange={(e) => { setPrevCmd(e.target.value); setSaved(false); }} rows={2} spellCheck={false} />
          </label>
          <label>
            Health URL (empty: the first URL the command prints)
            <input value={s.preview.url ?? ''} onChange={(e) => patch({ preview: { ...s.preview, url: e.target.value } })} />
          </label>
        </fieldset>
        <div className="form-actions">
          <button className="primary" disabled={act.busy !== ''} onClick={save}>
            Save development settings
          </button>
          {saved ? <span role="status">Saved.</span> : null}
        </div>
      </div>
    </section>
  );
}
