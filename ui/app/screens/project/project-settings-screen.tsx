// Project settings: what the project is, how its work is delivered (merge locally or open a pull request), the extra
// verification checks, the development flow and, last, deleting the project (decision P51).

import { css, navigate, on, type Handle } from 'remix/component';
import { FolderGit2, Settings2, Trash2 } from 'lucide';
import { projectHasRepo, type MergeStrategy, type Project, type ProjectMode } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { Select, selectOptions } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { AgentConfigPanel } from './project-agent-config.tsx';
import { CheckSettingsPanel, DevSettingsPanel } from './project-dev.tsx';
import { RunModePanel } from './project-run-mode.tsx';
import { SandboxPanel } from './project-sandbox.tsx';
import { MODES, STRATEGIES } from './project.ts';
import { AttachRepository, BriefPanel } from './project-repo.tsx';
import { SettingRow, SettingsGroup, errorPad, footerCss } from './settings-kit.tsx';

const colCss = css({ display: 'grid', gap: t.s8, width: '100%', maxWidth: '820px', margin: '0 auto' });
const valueCss = css({ fontSize: t.textMd, overflowWrap: 'anywhere', textAlign: 'right' });

const dangerHead = css({ '& > header h2': { color: t.danger } });
const deleteBody = css({
  display: 'grid',
  gap: t.s3,
  padding: `${t.s3} ${t.s4}`,
  fontSize: t.textMd,
  '& p, & ul': { margin: '0' },
  '& ul': { paddingLeft: t.s5, display: 'grid', gap: '2px', color: t.textMuted },
  '& .confirm': { display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, background: t.dangerSoft },
  '& .confirm input': { maxWidth: '360px' },
  '& .row': { display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap' },
});

/**
 * Deleting the project: what goes and what stays, then the person types the project's name to confirm. The core refuses while
 * work runs in the project and checks the name again. Afterwards the workspace Home opens.
 */
export function DeleteProject(handle: Handle<{ project: Project; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let open = false;
  let typed = '';
  const inputId = `delete-confirm-${handle.id}`;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const remove = async () => {
    const p = handle.props.project;
    const r = await act.run('delete', () => s.client.projectDelete(p.id, typed));
    if (!r) return;
    s.ui.flash(
      r.warnings.length > 0 ? `${r.name} was deleted. Not cleaned up: ${r.warnings.join('; ')}` : `${r.name} was deleted.`,
      r.warnings.length > 0 ? 'warn' : 'ok',
    );
    s.workspace.projects.reload();
    const home = routes.workspace.href();
    if (handle.props.onNavigate) handle.props.onNavigate(home);
    else void navigate(home);
  };
  return () => {
    const p = handle.props.project;
    const matches = typed.trim() === p.name;
    return (
      <section mix={dangerHead} data-testid="delete-project">
        <SettingsGroup title="Delete project" label="Delete project" description="Removes the project from Drydock. This cannot be undone.">
          <div mix={deleteBody}>
            <p>Deleting {p.name} removes, from Drydock only:</p>
            <ul>
              <li>its channels, direct messages and their messages</li>
              <li>its tasks, their runs and the worktrees Drydock made for them</li>
              <li>its agents, memory, skills, requirements, milestones, budgets and settings</li>
            </ul>
            <p>
              {projectHasRepo(p) ? (
                <>
                  The repository <code>{p.repoPath}</code>, its branches and its commits are not touched.{' '}
                </>
              ) : null}
              Your chats stay, without the project. The library, integrations and connections stay. To keep a copy,{' '}
              <a href={routes.data.href()}>export the project</a> first.
            </p>
            {act.error ? <ErrorNote error={act.error} what="Not deleted" /> : null}
            {open ? (
              <form class="confirm" mix={on('submit', (e) => (e.preventDefault(), matches ? void remove() : undefined))}>
                <label for={inputId}>
                  Type <strong>{p.name}</strong> to confirm
                </label>
                <input id={inputId} autocomplete="off" spellcheck={false} value={typed} mix={on('input', (e) => set(() => (typed = e.currentTarget.value)))} data-testid="delete-confirm" />
                <div class="row">
                  <Button type="submit" variant="danger" size="sm" icon={Trash2} disabled={!matches || !!act.busy} data-testid="delete-project-confirm">
                    {act.busy ? 'Deleting...' : `Delete ${p.name}`}
                  </Button>
                  <Button size="sm" mix={[on('click', () => set(() => ((open = false), (typed = ''))))]}>
                    Cancel
                  </Button>
                </div>
              </form>
            ) : (
              <div class="row">
                <Button variant="danger" size="sm" icon={Trash2} data-testid="delete-project-open" mix={[on('click', () => set(() => (open = true)))]}>
                  Delete project…
                </Button>
              </div>
            )}
          </div>
        </SettingsGroup>
      </section>
    );
  };
}

function Delivery(handle: Handle<{ project: Project }>) {
  const s = services(handle);
  const act = action(handle);
  let mode = handle.props.project.mode as string;
  let strategy = handle.props.project.mergeStrategy as string;
  let base = '';
  let saved = false;
  const modeId = `mode-${handle.id}`;
  const stratId = `strategy-${handle.id}`;

  const save = async () => {
    const p = handle.props.project;
    saved = false;
    const ok = await act.run('save', async () => {
      await s.client.projectUpdate(p.id, mode as ProjectMode, strategy as MergeStrategy);
      return true;
    });
    if (!ok) return;
    saved = true;
    void handle.update();
    s.workspace.projects.reload();
  };

  return () => {
    const p = handle.props.project;
    // a change from the core (or another project) resets the form to what is stored
    const key = `${p.id}:${p.mode}:${p.mergeStrategy}`;
    if (key !== base) {
      if (base && !base.startsWith(`${p.id}:`)) saved = false;
      base = key;
      mode = p.mode;
      strategy = p.mergeStrategy;
    }
    const dirty = mode !== p.mode || strategy !== p.mergeStrategy;
    const noRepo = !projectHasRepo(p);
    return (
      <SettingsGroup title="Delivery" label="Delivery" description={noRepo ? 'Attach a repository first: until then there is nothing to deliver to.' : 'How an approved task reaches your branch.'}>
        <form mix={on('submit', (e) => (e.preventDefault(), void save()))}>
          {act.error ? (
            <div mix={errorPad}>
              <ErrorNote error={act.error} what="Not saved" />
            </div>
          ) : null}
          <SettingRow label="Mode" for={modeId} description="Merge in this repository, or open a pull request on GitHub.">
            <Select id={modeId} label="Mode" value={mode} disabled={noRepo} options={selectOptions(MODES)} onChange={(v) => ((mode = v), (saved = false), void handle.update())} />
          </SettingRow>
          <SettingRow label="Merge strategy" for={stratId} description="How the task branch is combined with the default branch.">
            <Select id={stratId} label="Merge strategy" value={strategy} disabled={noRepo} options={selectOptions(STRATEGIES)} onChange={(v) => ((strategy = v), (saved = false), void handle.update())} />
          </SettingRow>
          <div mix={footerCss}>
            <span class="hint">Changing this affects tasks started afterwards. The core refuses pull request mode when the repository cannot do pull requests.</span>
            {saved ? <span role="status">Saved.</span> : null}
            <Button type="submit" variant="primary" size="sm" disabled={!!act.busy || !dirty || noRepo}>
              Save
            </Button>
          </div>
        </form>
      </SettingsGroup>
    );
  };
}

export function ProjectSettingsScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  return () => {
    const p = s.workspace.project;
    if (!p) {
      return (
        <Page title="Project settings" icon={Settings2}>
          <Empty title="No project selected" icon={FolderGit2}>
            <p>
              <a href={routes.projectNew.href()}>Create a project</a> to change its settings.
            </p>
          </Empty>
        </Page>
      );
    }
    return (
      <Page title="Project settings" icon={Settings2} subtitle={p.name}>
        <div mix={colCss}>
          <SettingsGroup title="General" label="General" description="The repository this project works on. Drydock works in separate worktrees and merges only after you approve.">
            <SettingRow label="Name">
              <span mix={valueCss}>{p.name}</span>
            </SettingRow>
            <SettingRow label="Repository">
              {projectHasRepo(p) ? (
                <code mix={valueCss}>{p.repoPath}</code>
              ) : (
                <span mix={valueCss} style={{ color: 'var(--text-muted)' }} data-testid="no-repo">
                  No repository yet
                </span>
              )}
            </SettingRow>
            {projectHasRepo(p) ? (
              <SettingRow label="Default branch">
                <code mix={valueCss}>{p.defaultBranch}</code>
              </SettingRow>
            ) : null}
          </SettingsGroup>
          {projectHasRepo(p) ? null : <AttachRepository key={`attach-${p.id}`} project={p} />}
          <BriefPanel key={`brief-${p.id}`} project={p} />
          <Delivery project={p} />
          <CheckSettingsPanel key={`checks-${p.id}`} projectId={p.id} />
          <DevSettingsPanel key={`dev-${p.id}`} projectId={p.id} />
          <RunModePanel key={`runmode-${p.id}`} projectId={p.id} />
          {projectHasRepo(p) ? <SandboxPanel key={`sandbox-${p.id}`} projectId={p.id} /> : null}
          {projectHasRepo(p) ? <AgentConfigPanel key={`agentconfig-${p.id}`} projectId={p.id} /> : null}
          <DeleteProject key={`delete-${p.id}`} project={p} />
        </div>
      </Page>
    );
  };
}
