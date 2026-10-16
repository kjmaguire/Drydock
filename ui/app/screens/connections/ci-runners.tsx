// CI runners (decision P56): GitHub Actions self-hosted runners for the person's private repositories, kept by Drydock on this
// PC. Windows jobs run on the PC in a fresh folder each (on a Dev Drive when there is one); Linux jobs run in a fresh container
// each. Every runner takes one job, then its files go and a new one waits. Runners only run CI and report to GitHub: fixing
// and pushing stay with Drydock's tasks. It sits on the Connections page, after the MCP gateway. The folder for Windows jobs
// is chosen with the window's folder dialog (ChooseFolder; the choice is saved at once) or typed and saved with "Use this
// folder"; when no Dev Drive was found the core's reason is shown (the check needs no administrator rights).

import { css, on, type Handle } from 'remix/component';
import type { RunnerKindName, RunnerSlot, RunnerState } from '../../api/runnersClient.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { listCard, Section } from '../../ui/layout.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import { ChooseFolder } from '../shared/choose-folder.tsx';

const kindsCss = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const kindCss = css({
  display: 'grid',
  gap: '4px',
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgApp,
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& strong': { fontWeight: '600' },
  '& p': { margin: '0', fontSize: t.textSm },
  '& code': { fontFamily: t.mono, fontSize: t.textSm },
});
const rootCss = css({
  display: 'grid',
  gap: t.s2,
  '& p': { margin: '0', fontSize: t.textSm },
  '& .line': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& input': { flex: '1', minWidth: '220px', fontFamily: t.mono, fontSize: t.textSm },
});
const slotCss = css({
  flexWrap: 'wrap',
  '& .repo': { fontWeight: '600', fontFamily: t.mono, fontSize: t.textSm },
  '& .what': { flexBasis: '100%', fontSize: t.textSm, color: t.textMuted, overflowWrap: 'anywhere' },
  '& .acts': { marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s2 },
  '& pre': { flexBasis: '100%', margin: '0', maxHeight: '220px', overflow: 'auto', padding: t.s2, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textXs },
});
const addCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  '& .line': { display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' },
  '& input[type="text"]': { flex: '1', minWidth: '220px', fontFamily: t.mono, fontSize: t.textSm },
  '& fieldset': { border: '0', margin: '0', padding: '0', display: 'flex', gap: t.s3, flexWrap: 'wrap', alignItems: 'center' },
  '& legend': { float: 'left', marginRight: t.s2, fontSize: t.textSm, fontWeight: '500' },
  '& label.radio': { display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: t.textMd },
});

const KIND_NAME: Record<RunnerKindName, string> = { windows: 'Windows', linux: 'Linux' };

/** The state of a runner in words, and the badge's tone. */
export function runnerStateText(s: RunnerSlot): { text: string; tone: Tone } {
  const states: Record<RunnerState, { text: string; tone: Tone }> = {
    paused: { text: 'off', tone: 'muted' },
    starting: { text: 'getting ready', tone: 'info' },
    idle: { text: 'waiting for a job', tone: 'ok' },
    busy: { text: s.job ? `running ${s.job}` : 'running a job', tone: 'accent' },
    blocked: { text: 'cannot run now', tone: 'warn' },
    error: { text: 'failed, trying again', tone: 'err' },
  };
  return states[s.state] ?? { text: s.state, tone: 'muted' };
}

export function gb(bytes: number): string {
  return `${(bytes / 2 ** 30).toFixed(bytes >= 100 * 2 ** 30 ? 0 : 1)} GB`;
}

export function CiRunners(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const v = query(handle, { load: () => s.client.runnersGet(), when: (e) => e.type === 'runners.changed', debounceMs: 150 });
  const act = action(handle);
  let repo = '';
  let kind: RunnerKindName | '' = '';
  let accept = false;
  let root: string | undefined;
  const logs: Record<string, string[] | undefined> = {};
  // what the person just set, shown until the core's answer arrives (so the box does not jump back meanwhile)
  const want: Record<string, boolean | undefined> = {};

  const done = (r: unknown) => {
    if (r) v.reload();
    void handle.update();
  };
  const add = async () => {
    if (!kind) return;
    const r = await act.run('add', () => s.client.runnersAdd(repo.trim(), kind as RunnerKindName, accept));
    if (r) {
      repo = '';
      accept = false;
    }
    done(r);
  };
  const enable = async (sl: RunnerSlot, on: boolean) => {
    want[sl.key] = on;
    void handle.update();
    const r = await act.run(`enable:${sl.key}`, () => s.client.runnersEnable(sl.repo, sl.kind, on));
    // the answer is the new view: show it before the optimistic flag goes, else the box flips back until the reload lands
    if (r) v.set(r);
    delete want[sl.key];
    done(r);
  };
  const remove = async (sl: RunnerSlot) => {
    delete logs[sl.key];
    done(await act.run(`remove:${sl.key}`, () => s.client.runnersRemove(sl.repo, sl.kind)));
  };
  const showLog = async (sl: RunnerSlot) => {
    if (logs[sl.key]) {
      delete logs[sl.key];
      void handle.update();
      return;
    }
    const lines = await act.run(`log:${sl.key}`, () => s.client.runnersLog(sl.repo, sl.kind));
    if (lines) logs[sl.key] = lines;
    void handle.update();
  };
  const setRoot = async (path: string) => {
    const r = await act.run('root', () => s.client.runnersRootSet(path));
    if (r) root = undefined;
    done(r);
  };
  // a folder from the window's dialog is the person's decision: it is saved at once (and stays in the box if the save fails)
  const pickRoot = (path: string) => {
    root = path;
    void handle.update();
    void setRoot(path);
  };

  return () => {
    const { readOnly } = s.workspace.license;
    const data = v.data;
    const busy = readOnly || act.busy !== '';
    const kinds = data?.kinds ?? [];
    const avail = kinds.filter((k) => k.available);
    const chosen = kinds.find((k) => k.kind === kind);
    if (kind && !chosen?.available) kind = '';
    if (!kind && avail.length === 1) kind = avail[0]!.kind;
    const canAdd = !busy && repo.trim() !== '' && kind !== '' && (kind !== 'windows' || accept) && (data?.slots.length ?? 0) < (data?.max ?? 8);
    const winOn = (data?.slots ?? []).some((x) => x.kind === 'windows' && x.enabled);
    const rootTyped = root ?? '';
    const rootSource =
      data?.rootSource === 'devdrive'
        ? `on your Dev Drive ${data.devDrives[0]?.letter ?? ''}${data.devDrives[0]?.trusted ? ' (trusted: Microsoft Defender scans it in performance mode)' : ''}`
        : data?.rootSource === 'chosen'
          ? 'the folder you chose'
          : data?.devDriveReason
            ? "in Drydock's own folder"
            : "in Drydock's own folder (no Dev Drive was found)";
    const devDriveReason = data?.rootSource === 'default' ? data.devDriveReason : undefined;
    return (
      <Section
        label="CI runners"
        testId="ci-runners-section"
        title="CI runners"
        description="GitHub Actions self-hosted runners for your private repositories, on this PC. Each runner takes one job; then its files are deleted and a fresh one waits. Runners only run CI and report the result to GitHub: when CI fails, Drydock's task reads the failure, fixes it and pushes, as with GitHub's own runners."
      >
        <ErrorNote error={v.error ?? act.error} what="CI runners" />
        <Notice tone="warn" testId="runner-private-only">
          Private repositories only. On a public repository anyone's pull request could run its own code on this PC, so Drydock refuses it.
        </Notice>
        <ul mix={kindsCss} aria-label="Where jobs can run">
          {kinds.map((k) => (
            <li key={k.kind} mix={kindCss} data-testid="runner-kind" data-kind={k.kind} data-available={String(k.available)}>
              <div class="head">
                <strong>{KIND_NAME[k.kind]}</strong>
                {k.available ? <Badge tone="ok">available</Badge> : <Badge tone="warn">not available</Badge>}
                {k.freeBytes ? <span mix={muted}>{gb(k.freeBytes)} free</span> : null}
              </div>
              <p mix={muted}>{k.where}</p>
              {k.available ? (
                <p>
                  In a workflow: <code>runs-on: {k.runsOn}</code>
                </p>
              ) : k.reason ? (
                <p mix={muted}>{k.reason}</p>
              ) : null}
            </li>
          ))}
        </ul>

        {data?.root ? (
          <form
            mix={[
              rootCss,
              on('submit', (e) => {
                e.preventDefault();
                if (!busy && rootTyped.trim()) void setRoot(rootTyped.trim());
              }),
            ]}
            data-testid="runner-root"
          >
            <p>
              Windows jobs run in <span mix={mono}>{data.root}</span>, {rootSource}. A job waits while less than {gb(data.minFreeBytes)} is free there.
            </p>
            {devDriveReason ? (
              <p mix={muted} data-testid="runner-devdrive-reason">
                No Dev Drive was found: {devDriveReason}.
              </p>
            ) : null}
            <div class="line">
              <input
                type="text"
                aria-label="Folder for Windows jobs"
                data-testid="runner-root-input"
                spellcheck={false}
                placeholder="another folder, for example D:\DrydockRunners"
                value={rootTyped}
                disabled={busy || winOn}
                mix={on('input', (e) => {
                  root = e.currentTarget.value;
                  void handle.update();
                })}
              />
              <ChooseFolder title="Choose the folder for Windows jobs" testId="runner-root-browse" disabled={busy || winOn} onPick={pickRoot} />
              <Button type="submit" variant="secondary" size="sm" data-testid="runner-root-save" disabled={busy || winOn || !rootTyped.trim()}>
                Use this folder
              </Button>
              {data.rootSource === 'chosen' ? (
                <Button type="button" variant="ghost" size="sm" data-testid="runner-root-reset" disabled={busy || winOn} mix={on('click', () => void setRoot(''))}>
                  Back to automatic
                </Button>
              ) : null}
            </div>
            {winOn ? <p mix={muted}>Switch the Windows runners off to change the folder.</p> : null}
          </form>
        ) : null}

        <ul mix={listCard} data-testid="runner-slots" aria-label="Your runners">
          {(data?.slots ?? []).map((sl) => {
            const st = runnerStateText(sl);
            const lines = logs[sl.key];
            return (
              <li key={sl.key} mix={slotCss} data-testid="runner-slot" data-key={sl.key} data-state={sl.state}>
                <span class="repo">{sl.repo}</span>
                <Badge>{KIND_NAME[sl.kind]}</Badge>
                <Badge tone={st.tone} testId="runner-state">
                  {st.text}
                </Badge>
                <span class="acts">
                  <Checkbox label="On" checked={want[sl.key] ?? sl.enabled} disabled={busy} onChange={(c) => void enable(sl, c)} />
                  <Button type="button" variant="ghost" size="sm" data-testid="runner-log" disabled={act.busy !== ''} mix={on('click', () => void showLog(sl))}>
                    {lines ? 'Hide output' : 'Output'}
                  </Button>
                  <Button type="button" variant="ghost" size="sm" data-testid="runner-remove" disabled={busy} mix={on('click', () => void remove(sl))}>
                    Remove
                  </Button>
                </span>
                <span class="what" data-testid="runner-what">
                  {sl.reason ? `${sl.reason} ` : ''}
                  {sl.lastJob ? `Last job: ${sl.lastJob.name}, ${sl.lastJob.result.toLowerCase()}. ` : ''}
                  {sl.jobs > 0 ? `${sl.jobs} job${sl.jobs === 1 ? '' : 's'} since Drydock started. ` : ''}
                  {sl.version ? `Runner ${sl.version}.` : ''}
                </span>
                {lines ? <pre data-testid="runner-output">{lines.length > 0 ? lines.join('\n') : 'No output yet.'}</pre> : null}
              </li>
            );
          })}
          {data && data.slots.length === 0 ? <li mix={muted}>No runners yet. Add one below for a private repository.</li> : null}
        </ul>

        {data && avail.length > 0 ? (
          <form
            mix={[
              addCss,
              on('submit', (e) => {
                e.preventDefault();
                if (canAdd) void add();
              }),
            ]}
            data-testid="runner-add"
          >
            <div class="line">
              <input
                type="text"
                aria-label="Repository"
                data-testid="runner-repo"
                spellcheck={false}
                placeholder="owner/name, or the repository's GitHub address"
                value={repo}
                disabled={busy}
                mix={on('input', (e) => {
                  repo = e.currentTarget.value;
                  void handle.update();
                })}
              />
            </div>
            <fieldset>
              <legend>Jobs run on</legend>
              {kinds.map((k) => (
                <label key={k.kind} class="radio">
                  <input
                    type="radio"
                    name="runner-kind"
                    value={k.kind}
                    data-testid={`runner-kind-${k.kind}`}
                    checked={kind === k.kind}
                    disabled={busy || !k.available}
                    mix={on('change', () => {
                      kind = k.kind;
                      void handle.update();
                    })}
                  />
                  {KIND_NAME[k.kind]}
                </label>
              ))}
            </fieldset>
            {kind === 'windows' ? (
              <Checkbox
                label="Jobs on this runner run on this PC as me, with my files and my network. I accept this for this repository."
                description="Windows has no container for each job like Linux has. Use it for repositories whose workflows you trust."
                checked={accept}
                disabled={busy}
                onChange={(c) => {
                  accept = c;
                  void handle.update();
                }}
              />
            ) : null}
            <div class="line">
              <Button type="submit" variant="primary" size="sm" data-testid="runner-add-button" disabled={!canAdd}>
                {act.busy === 'add' ? 'Adding…' : 'Add runner'}
              </Button>
              <span mix={muted}>Drydock checks with GitHub that the repository is private and that you administer it.</span>
            </div>
          </form>
        ) : null}
      </Section>
    );
  };
}
