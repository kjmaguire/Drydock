// CI runners (decision P56): GitHub Actions self-hosted runners for the person's private repositories, kept by Drydock on this
// PC. Windows jobs run on the PC in a fresh folder each (on a Dev Drive when there is one); Linux jobs run in a fresh container
// each. Every runner takes one job, then its files go and a new one waits. Runners only run CI and report to GitHub: fixing
// and pushing stay with Drydock's tasks. On the GitHub page (decision P98, after GitHub's own runners list): a header with where
// jobs can run as chips and "Add runner", which opens the add form in a dialog; the runners as a table (repository, runs on,
// state in words, last job, On, and a menu with the output and Remove, which asks first); the folder for Windows jobs as one
// settings row, chosen with the window's folder dialog (ChooseFolder; saved at once) or typed and saved with "Use this folder",
// with the core's reason when no Dev Drive was found; and the private-repositories-only rule as one line.

import { css, on, ref, type Handle } from 'remix/component';
import { Ellipsis, Lock, Plus } from 'lucide';
import type { RunnerKindName, RunnerSlot, RunnerState } from '../../api/runnersClient.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { faint, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { tableCard } from '../../ui/layout.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { SettingRow, SettingsGroup, StatusWord } from '../../ui/master-detail.tsx';
import { Menu } from '../../ui/menu.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import { ChooseFolder } from '../shared/choose-folder.tsx';

const sectionCss = css({ display: 'grid', gap: t.s2, minWidth: '0', '& p': { margin: '0' } });
const barCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s2,
  '& h2': { margin: '0', fontSize: t.textSm, fontWeight: '600', color: t.textMuted },
  '& > [data-part="add"]': { marginLeft: 'auto' },
});
/** A chip of the header: a state dot and a few words, on a quiet outline. */
export const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  minHeight: '24px',
  padding: `0 ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  fontSize: t.textSm,
  whiteSpace: 'nowrap',
});
const chipListCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const tableCss = css({
  '& td': { verticalAlign: 'middle' },
  '& td[data-part="repo"]': { fontFamily: t.mono, fontSize: t.textSm, overflowWrap: 'anywhere' },
  '& td[data-part="what"]': { color: t.textMuted, fontSize: t.textSm, minWidth: '180px' },
  '& td[data-part="acts"]': { textAlign: 'right', whiteSpace: 'nowrap' },
  '& td[data-part="acts"] > *': { verticalAlign: 'middle' },
  '& pre': { margin: '0', maxHeight: '220px', overflow: 'auto', padding: t.s2, borderRadius: t.rMd, background: t.bgApp },
  '& tbody tr[data-part="output"]:hover td, & tbody tr[data-part="ask"]:hover td': { background: 'transparent' },
});
const askCss = css({ display: 'inline-flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, justifyContent: 'flex-end' });
const rootCss = css({
  display: 'grid',
  gap: t.s2,
  '& .line': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& input': { flex: '1', minWidth: '200px', fontFamily: t.mono, fontSize: t.textSm },
});
const ruleCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, color: t.textMuted, fontSize: t.textSm });
const addCss = css({
  display: 'grid',
  gap: t.s4,
  '& p': { margin: '0', fontSize: t.textSm },
  '& input[type="text"]': { width: '100%', fontFamily: t.mono, fontSize: t.textSm },
  '& fieldset': { border: '0', margin: '0', padding: '0', display: 'flex', gap: t.s3, flexWrap: 'wrap', alignItems: 'center' },
  '& legend': { float: 'left', marginRight: t.s2, fontSize: t.textSm, fontWeight: '500' },
  '& label.radio': { display: 'inline-flex', alignItems: 'center', gap: '6px', minHeight: '28px', fontSize: t.textMd },
  '& .line': { display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' },
});

const KIND_NAME: Record<RunnerKindName, string> = { windows: 'Windows', linux: 'Linux' };

/** The state of a runner in words, and its tone. */
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

/** A phrase as a cell starts it: its first letter in capitals. */
export const sentence = (s: string): string => (s ? s[0]!.toUpperCase() + s.slice(1) : s);

export function gb(bytes: number): string {
  return `${(bytes / 2 ** 30).toFixed(bytes >= 100 * 2 ** 30 ? 0 : 1)} GB`;
}

/** What a runner did and why it waits, in one line: its reason, its last job, its jobs and its version. */
export function runnerWhat(sl: RunnerSlot): string {
  return [
    sl.reason ? `${sl.reason}` : '',
    sl.lastJob ? `Last job: ${sl.lastJob.name}, ${sl.lastJob.result.toLowerCase()}.` : '',
    sl.jobs > 0 ? `${sl.jobs} job${sl.jobs === 1 ? '' : 's'} since Drydock started.` : '',
    sl.version ? `Runner ${sl.version}.` : '',
  ]
    .filter(Boolean)
    .join(' ');
}

/**
 * Removing a runner asks first (audit 2026-10-16, L40), as ConfirmAction does: "Yes, remove" beside "Keep", the pair named by the
 * question; Keep gets the focus, "Yes" sends once and is off until the removal has ended.
 */
function RemoveQuestion(handle: Handle<{ question: string; yesName: string; keepName: string; disabled: boolean; onYes: () => Promise<unknown>; onKeep: () => void }>) {
  let pending = false;
  return () => {
    const { question, yesName, keepName, disabled, onYes, onKeep } = handle.props;
    return (
      <span mix={askCss} role="group" aria-label={question}>
        <Button
          size="sm"
          variant="danger"
          disabled={disabled || pending}
          aria-label={yesName}
          data-testid="runner-remove-confirm"
          mix={on('click', async () => {
            if (pending) return;
            pending = true;
            void handle.update();
            try {
              await onYes();
            } finally {
              pending = false;
              if (!handle.signal.aborted) void handle.update();
            }
          })}
        >
          Yes, remove
        </Button>
        <Button size="sm" disabled={pending} aria-label={keepName} mix={[ref((n) => (n as HTMLElement).focus()), on('click', () => onKeep())]}>
          Keep
        </Button>
      </span>
    );
  };
}

export function CiRunners(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const v = query(handle, { load: () => s.client.runnersGet(), when: (e) => e.type === 'runners.changed', debounceMs: 150 });
  const act = action(handle);
  let adding = false; // the add dialog is open
  let repo = '';
  let kind: RunnerKindName | '' = '';
  let accept = false;
  let root: string | undefined;
  let asking = ''; // the runner whose removal is being asked about
  const logs: Record<string, string[] | undefined> = {};
  // what the person just set, shown until the core's answer arrives (so the box does not jump back meanwhile)
  const want: Record<string, boolean | undefined> = {};
  const menus: Record<string, HTMLElement | undefined> = {};

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
      adding = false;
      v.set(r);
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
    const r = await act.run(`remove:${sl.key}`, () => s.client.runnersRemove(sl.repo, sl.kind));
    asking = '';
    if (r) v.set(r);
    else handle.queueTask(() => menus[sl.key]?.focus()); // the runner is still there: back where the person was
    done(r);
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
  const close = () => {
    adding = false;
    if (act.error) act.clear();
    void handle.update();
  };

  return () => {
    const { readOnly } = s.workspace.license;
    const data = v.data;
    const busy = readOnly || act.busy !== '';
    const kinds = data?.kinds ?? [];
    const slots = data?.slots ?? [];
    const avail = kinds.filter((k) => k.available);
    const chosen = kinds.find((k) => k.kind === kind);
    if (kind && !chosen?.available) kind = '';
    if (!kind && avail.length === 1) kind = avail[0]!.kind;
    const full = slots.length >= (data?.max ?? 8);
    const canOpen = !!data && avail.length > 0 && !full && !readOnly;
    const canAdd = !busy && repo.trim() !== '' && kind !== '' && (kind !== 'windows' || accept) && !full;
    const winOn = slots.some((x) => x.kind === 'windows' && x.enabled);
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
    const addTitle = readOnly ? s.workspace.license.reason : avail.length === 0 ? 'No kind of runner can run on this PC now' : full ? `At most ${data?.max ?? 8} runners` : undefined;
    return (
      <section mix={sectionCss} aria-labelledby={`runners-${handle.id}`} data-testid="ci-runners-section">
        <div mix={barCss}>
          <h2 id={`runners-${handle.id}`}>CI runners</h2>
          <ul aria-label="Where jobs can run" mix={chipListCss}>
            {kinds.map((k) => (
              <li key={k.kind} mix={chipCss} data-testid="runner-kind" data-kind={k.kind} data-available={String(k.available)} title={`${k.where}. In a workflow: runs-on: ${k.runsOn}`}>
                <StatusWord tone={k.available ? 'ok' : 'warn'} text={`${KIND_NAME[k.kind]} · ${k.available ? (k.freeBytes ? `${gb(k.freeBytes)} free` : 'available') : 'not available'}`} />
              </li>
            ))}
          </ul>
          <span data-part="add">
            <Button size="sm" icon={Plus} disabled={!canOpen || act.busy !== ''} title={addTitle} data-testid="runner-add-open" mix={on('click', () => ((adding = true), act.clear()))}>
              Add runner
            </Button>
          </span>
        </div>
        <ErrorNote error={v.error ?? (adding ? undefined : act.error)} what="CI runners" />
        {v.loading && !data ? <Loading label="Reading the runners" /> : null}
        {kinds
          .filter((k) => !k.available && k.reason)
          .map((k) => (
            <p key={k.kind} mix={faint} data-testid="runner-kind-reason" data-kind={k.kind}>
              {KIND_NAME[k.kind]}: {k.reason}
            </p>
          ))}

        {data && slots.length === 0 ? (
          <p mix={muted} data-testid="runner-slots">
            No runners yet. Add one for a private repository.
          </p>
        ) : null}
        {slots.length > 0 ? (
          <div mix={tableCard}>
            <table mix={tableCss} data-testid="runner-slots" aria-label="Your runners">
              <thead>
                <tr>
                  <th scope="col">Repository</th>
                  <th scope="col">Runs on</th>
                  <th scope="col">Status</th>
                  <th scope="col">Last job</th>
                  <th scope="col">
                    <span class="sr-only">On and actions</span>
                  </th>
                </tr>
              </thead>
              {slots.map((sl) => {
                const st = runnerStateText(sl);
                const lines = logs[sl.key];
                const name = `the ${KIND_NAME[sl.kind]} runner of ${sl.repo}`;
                const k = kinds.find((x) => x.kind === sl.kind);
                return (
                  <tbody key={sl.key} data-testid="runner-slot" data-key={sl.key} data-state={sl.state}>
                    <tr>
                      <td data-part="repo">{sl.repo}</td>
                      <td title={k ? `runs-on: ${k.runsOn}` : undefined}>{KIND_NAME[sl.kind]}</td>
                      <td>
                        <StatusWord testId="runner-state" tone={st.tone} text={sentence(st.text)} />
                      </td>
                      <td data-part="what" data-testid="runner-what">
                        {runnerWhat(sl) || (sl.state === 'busy' ? '' : 'No job yet.')}
                      </td>
                      <td data-part="acts">
                        {asking === sl.key ? (
                          <RemoveQuestion
                            question={`Remove ${name}? It stops taking jobs, and adding it again sets it up from the start.`}
                            yesName={`Yes, remove ${name}`}
                            keepName={`Keep ${name}`}
                            disabled={busy}
                            onYes={() => remove(sl)}
                            onKeep={() => {
                              asking = '';
                              void handle.update();
                              handle.queueTask(() => menus[sl.key]?.focus());
                            }}
                          />
                        ) : (
                          <>
                            <Checkbox label="On" checked={want[sl.key] ?? sl.enabled} disabled={busy} onChange={(c) => void enable(sl, c)} />
                            <Menu
                              label={`Actions for ${name}`}
                              placement="bottom-end"
                              items={[
                                { name: 'output', label: lines ? 'Hide output' : 'Show output' },
                                { name: 'remove', label: 'Remove…', danger: true, disabled: busy, separatorBefore: true },
                              ]}
                              onSelect={(n) => {
                                if (n === 'output') void showLog(sl);
                                if (n === 'remove') {
                                  asking = sl.key;
                                  void handle.update();
                                }
                              }}
                              trigger={(m) => <IconButton icon={Ellipsis} label={`Actions for ${name}`} data-testid="runner-menu" mix={[m, ref((n) => (menus[sl.key] = n as HTMLElement))]} />}
                            />
                          </>
                        )}
                      </td>
                    </tr>
                    {lines ? (
                      <tr data-part="output">
                        <td colSpan={5}>
                          <pre data-testid="runner-output">{lines.length > 0 ? lines.join('\n') : 'No output yet.'}</pre>
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                );
              })}
            </table>
          </div>
        ) : null}

        {data?.root ? (
          <form
            mix={on('submit', (e) => {
              e.preventDefault();
              if (!busy && rootTyped.trim()) void setRoot(rootTyped.trim());
            })}
            data-testid="runner-root"
          >
            <SettingsGroup label="Windows jobs">
              <SettingRow
                label="Windows jobs folder"
                control={
                  <>
                    <ChooseFolder title="Choose the folder for Windows jobs" testId="runner-root-browse" disabled={busy || winOn} onPick={pickRoot} />
                    {data.rootSource === 'chosen' ? (
                      <Button type="button" variant="ghost" size="sm" data-testid="runner-root-reset" disabled={busy || winOn} mix={on('click', () => void setRoot(''))}>
                        Back to automatic
                      </Button>
                    ) : null}
                  </>
                }
              >
                <span mix={rootCss}>
                  <span>
                    Windows jobs run in <span mix={mono}>{data.root}</span>, {rootSource}. A job waits while less than {gb(data.minFreeBytes)} is free there.
                  </span>
                  {devDriveReason ? (
                    <span mix={faint} data-testid="runner-devdrive-reason">
                      No Dev Drive was found: {devDriveReason}.
                    </span>
                  ) : null}
                  <span class="line">
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
                    <Button type="submit" variant="secondary" size="sm" data-testid="runner-root-save" disabled={busy || winOn || !rootTyped.trim()}>
                      Use this folder
                    </Button>
                  </span>
                  {winOn ? <span mix={faint}>Switch the Windows runners off to change the folder.</span> : null}
                </span>
              </SettingRow>
            </SettingsGroup>
          </form>
        ) : null}

        <p mix={ruleCss} data-testid="runner-private-only">
          <Icon icon={Lock} size={13} />
          Private repositories only: on a public one anyone&apos;s pull request could run its code on this PC, so Drydock refuses it.
          <LearnMore topic="runners" />
        </p>

        <Dialog open={adding} title="Add a runner" onClose={close}>
          {adding && data ? (
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
              <ErrorNote error={act.error} what="Not added" />
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
              {chosen?.available ? (
                <p mix={muted} data-testid="runner-runs-on">
                  {chosen.where}. In a workflow: <code>runs-on: {chosen.runsOn}</code>
                </p>
              ) : null}
              {kind === 'windows' ? (
                <Checkbox
                  label="Jobs on this runner run on this PC as me, with my files and my network. I accept this for this repository."
                  description="Windows has no container for each job like Linux has. Use it for repositories whose workflows you trust. A workflow an agent adds to a task branch runs here too once the branch is pushed; Drydock asks you before it pushes a branch that changes workflows, and shows which."
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
                <span mix={faint}>Drydock checks with GitHub that the repository is private and that you administer it.</span>
              </div>
            </form>
          ) : null}
        </Dialog>
      </section>
    );
  };
}
