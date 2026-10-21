// Per-project verification checks (checks.settings.get/set) and development flow settings (projectdev.get/set), with the CI
// gate before a pull request is merged. Only the human changes them.

import { css, on, type Handle } from 'remix/component';
import { DEFAULT_SKILL_DELIVERY, type CheckSettings, type DevSettings } from '../../api/extTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { devBody, numOrUndef, parallelWarn } from './project.ts';
import { SettingRow, SettingsGroup, ToggleRow, errorPad, footerCss, noteRow, subhead } from './settings-kit.tsx';

const stackRow = css({ display: 'grid', gap: '6px', padding: `${t.s3} ${t.s4}`, borderTop: `1px solid ${t.border}`, '& label': { fontSize: t.textMd, fontWeight: '500' }, '& textarea': { fontFamily: t.mono, fontSize: t.textSm } });
const numCss = css({ width: '96px', textAlign: 'right' });
const warnNote = css({ color: t.warning });
const choiceCss = css({
  display: 'grid',
  gap: t.s3,
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  '& > p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
  '& label': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '500', color: t.text },
  '& label + p': { margin: '2px 0 0 24px', fontSize: t.textSm, color: t.textMuted },
});

export function CheckSettingsPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.checksSettingsGet(handle.props.projectId), key: () => handle.props.projectId, when: (e) => e.type.startsWith('project.') });
  const act = action(handle);
  let cur: CheckSettings | undefined;
  let loaded: CheckSettings | undefined;
  let saved = false;
  const patch = (p: Partial<CheckSettings>) => {
    cur = { ...cur!, ...p };
    saved = false;
    void handle.update();
  };
  const save = async () => {
    const r = await act.run('save', () => s.client.checksSettingsSet(handle.props.projectId, cur!));
    if (r) {
      cur = r;
      saved = true;
      void handle.update();
    }
  };
  return () => {
    if (q.data && q.data !== loaded) {
      loaded = q.data;
      cur = q.data;
    }
    const c = cur;
    return (
      <SettingsGroup
        title="Verification checks"
        label="Verification checks"
        description="Build, test and lint always run. These add checks to every task. A check whose tool is missing is reported as skipped, never as passed."
      >
        {!c ? (
          <div mix={errorPad}>
            <ErrorNote error={q.error} what="Could not load check settings" />
            {q.error ? null : <Loading />}
          </div>
        ) : (
          <>
            {act.error ? (
              <div mix={errorPad}>
                <ErrorNote error={act.error} what="Not saved" />
              </div>
            ) : null}
            <ToggleRow label="Secrets scan of the diff" description="Looks for keys and tokens in what the agent changed." checked={c.secrets} onChange={(v) => patch({ secrets: v })} />
            <ToggleRow label="Dependency vulnerability scan" description="Checks the project's dependencies against known vulnerabilities." checked={c.deps} onChange={(v) => patch({ deps: v })} />
            <ToggleRow label="Offline only (no database download)" checked={c.depsOffline} disabled={!c.deps} onChange={(v) => patch({ depsOffline: v })} />
            <div mix={footerCss}>
              <span class="hint" />
              {saved ? <span role="status">Saved.</span> : null}
              <Button variant="primary" size="sm" disabled={!!act.busy} mix={on('click', () => void save())}>
                Save check settings
              </Button>
            </div>
          </>
        )}
      </SettingsGroup>
    );
  };
}

export function DevSettingsPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.projectdevGet(handle.props.projectId), key: () => handle.props.projectId });
  const act = action(handle);
  let cur: DevSettings | undefined;
  let loaded: DevSettings | undefined;
  let e2eCmd = '';
  let prevCmd = '';
  let saved = false;
  const load = (d: DevSettings) => {
    cur = d;
    e2eCmd = (d.e2e.command ?? []).join('\n');
    prevCmd = (d.preview.command ?? []).join('\n');
  };
  const touch = (f: () => void) => {
    f();
    saved = false;
    void handle.update();
  };
  const patch = (p: Partial<DevSettings>) => touch(() => (cur = { ...cur!, ...p }));
  const save = async () => {
    const r = await act.run('save', () => s.client.projectdevSet(handle.props.projectId, devBody(cur!, e2eCmd, prevCmd)));
    if (r) {
      load(r);
      saved = true;
      void handle.update();
    }
  };
  const parId = `par-${handle.id}`;
  const stallId = `stall-${handle.id}`;
  const e2eId = `e2e-${handle.id}`;
  const prevId = `prev-${handle.id}`;
  const urlId = `url-${handle.id}`;
  const ciName = `ci-${handle.id}`;
  const skName = `skills-${handle.id}`;
  return () => {
    if (q.data && q.data !== loaded) {
      loaded = q.data;
      load(q.data);
    }
    const d = cur;
    const ciNone = d?.ciMode === 'none';
    const skills = d?.skillDelivery ?? DEFAULT_SKILL_DELIVERY;
    return (
      <SettingsGroup
        title="Development flow"
        label="Development flow"
        description="How tasks run in this project: how many agents work at once, the scheduler, room replies, what CI must do before a merge, and the end-to-end and preview steps."
      >
        {!d ? (
          <div mix={errorPad}>
            <ErrorNote error={q.error} what="Could not load development settings" />
            {q.error ? null : <Loading />}
          </div>
        ) : (
          <>
            {act.error ? (
              <div mix={errorPad}>
                <ErrorNote error={act.error} what="Not saved" />
              </div>
            ) : null}
            <SettingRow label="Maximum parallel agent turns" for={parId} description="Agent turns that may run at the same time in this project.">
              <input
                id={parId}
                inputMode="numeric"
                value={String(d.maxParallel)}
                mix={[numCss, on('input', (e) => patch({ maxParallel: numOrUndef(e.currentTarget.value) ?? 0 }))]}
              />
            </SettingRow>
            {parallelWarn(d.maxParallel) ? (
              <p mix={[noteRow, warnNote]} data-testid="parallel-note">
                Anthropic suggests 3 to 5 at once: cost grows with every active agent, and more rarely finishes sooner.
</p>
            ) : null}
            <ToggleRow label="Start ready tasks automatically (scheduler)" checked={d.autoSchedule} onChange={(v) => patch({ autoSchedule: v })} />
            <ToggleRow label="Make a task wait while an earlier task is changing the same code" checked={d.collisionGuard !== false} onChange={(v) => patch({ collisionGuard: v })} />
            <ToggleRow
              label="Let Claude workers run this project's own checks (build, test, lint) while they work"
              checked={d.workerChecks === true}
              onChange={(v) => patch({ workerChecks: v })}
            />
            {d.workerChecks ? (
              <p mix={[noteRow, warnNote]} data-testid="worker-checks-note">
                Workers may type only the exact check commands. They run the project's own code, which the worker can change in the same turn, so in practice a worker can run any program with your user's rights. There is no operating-system sandbox. Turn this on only where you accept that.
</p>
            ) : null}
            <ToggleRow label="Agents answer in channels and direct messages" checked={d.respond} onChange={(v) => patch({ respond: v })} />
            <SettingRow
              label="Team lead: minutes without progress before it checks a task"
              for={stallId}
              description="The lead checks a running task with no progress after this many minutes. Empty or 0 means 15; at most 1440."
            >
              <input
                id={stallId}
                inputMode="numeric"
                placeholder="15"
                value={d.leadStallMinutes ? String(d.leadStallMinutes) : ''}
                mix={[numCss, on('input', (e) => patch({ leadStallMinutes: numOrUndef(e.currentTarget.value) ?? 0 }))]}
              />
            </SettingRow>
            {(d.leadStallMinutes ?? 0) > 1440 || (d.leadStallMinutes ?? 0) < 0 ? (
              <p mix={[noteRow, warnNote]} data-testid="stall-note">
                The team lead's stall time is between 0 (15 minutes) and 1440 minutes.
              </p>
            ) : null}

            <h3 mix={subhead}>Skills for Claude agents</h3>
            <div role="radiogroup" aria-label="Skills for Claude agents" mix={choiceCss} data-testid="skill-delivery">
              <p>How a Claude agent gets the skills it has been given. Other agent CLIs always get the matching ones at the start and the rest on request.</p>
              <div>
                <label>
                  <input
                    type="radio"
                    name={skName}
                    value="relevant"
                    checked={skills === 'relevant'}
                    aria-describedby={`${skName}-relevant`}
                    mix={on('change', () => patch({ skillDelivery: 'relevant' }))}
                  />
                  Only the ones the task needs{DEFAULT_SKILL_DELIVERY === 'relevant' ? ' (default)' : ''}
                </label>
                <p id={`${skName}-relevant`}>
                  Only the repository's own skills and path-matched imports go into each turn; the rest are listed and opened on request. Fewer tokens.
</p>
              </div>
              <div>
                <label>
                  <input
                    type="radio"
                    name={skName}
                    value="all"
                    checked={skills === 'all'}
                    aria-describedby={`${skName}-all`}
                    mix={on('change', () => patch({ skillDelivery: 'all' }))}
                  />
                  All of them, in every turn{DEFAULT_SKILL_DELIVERY === 'all' ? ' (default)' : ''}
                </label>
                <p id={`${skName}-all`}>
                  Every skill goes into every turn. Large, unrelated skills cost tokens on every request.
</p>
              </div>
            </div>

            <h3 mix={subhead}>CI before merge</h3>
            <div role="radiogroup" aria-label="CI before merge" mix={choiceCss} data-testid="ci-mode">
              <p>In pull request mode, what CI must do before Drydock merges. Drydock never asks GitHub to bypass branch protection.</p>
              <div>
                <label>
                  <input
                    type="radio"
                    name={ciName}
                    value="required"
                    checked={!ciNone}
                    aria-describedby={`${ciName}-required`}
                    mix={on('change', () => patch({ ciMode: 'required' }))}
                  />
                  Required (default)
                </label>
                <p id={`${ciName}-required`}>
                  Merges only after CI passed on the reviewed commit. With no CI after 2 minutes you are asked what to do.
</p>
              </div>
              <div>
                <label>
                  <input
                    type="radio"
                    name={ciName}
                    value="none"
                    checked={ciNone}
                    aria-describedby={`${ciName}-none`}
                    mix={on('change', () => patch({ ciMode: 'none' }))}
                  />
                  This repository has no CI: merge after the review alone
                </label>
                <p id={`${ciName}-none`}>
                  Only for a repository with no CI. Checks that do report must still pass, and branch protection still applies. Your name and the time are recorded.
</p>
              </div>
            </div>
            {ciNone ? (
              <p mix={noteRow} data-testid="ci-none-note">
                {d.ciNoneBy
                  ? `Declared by ${d.ciNoneBy} on ${formatDateTime(d.ciNoneAt)}.`
                  : 'Not saved yet: your name and the time are recorded when you save.'}
              </p>
            ) : null}

            <h3 mix={subhead}>End-to-end check</h3>
            <ToggleRow label="Run e2e tests in the task worktree" checked={d.e2e.enabled} onChange={(v) => patch({ e2e: { ...d.e2e, enabled: v } })} />
            <div mix={stackRow}>
              <label for={e2eId}>Command (one argument per line; empty detects Taskfile, package.json script or Playwright config)</label>
              <textarea id={e2eId} rows={2} spellcheck={false} value={e2eCmd} mix={on('input', (e) => touch(() => (e2eCmd = e.currentTarget.value)))} />
            </div>

            <h3 mix={subhead}>Preview</h3>
            <ToggleRow label="Start a preview of the task before approval" checked={d.preview.enabled} onChange={(v) => patch({ preview: { ...d.preview, enabled: v } })} />
            <div mix={stackRow}>
              <label for={prevId}>Command (one argument per line)</label>
              <textarea id={prevId} rows={2} spellcheck={false} value={prevCmd} mix={on('input', (e) => touch(() => (prevCmd = e.currentTarget.value)))} />
            </div>
            <div mix={stackRow}>
              <label for={urlId}>Health URL (empty: the first URL the command prints)</label>
              <input id={urlId} spellcheck={false} value={d.preview.url ?? ''} mix={on('input', (e) => patch({ preview: { ...d.preview, url: e.currentTarget.value } }))} />
            </div>

            <div mix={footerCss}>
              <span class="hint" />
              {saved ? <span role="status">Saved.</span> : null}
              <Button variant="primary" size="sm" disabled={!!act.busy} mix={on('click', () => void save())}>
                Save development settings
              </Button>
            </div>
          </>
        )}
      </SettingsGroup>
    );
  };
}
