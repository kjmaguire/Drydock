// Project settings: what the project is, how its work is delivered (merge locally or open a pull request), the extra
// verification checks and the development flow.

import { css, on, type Handle } from 'remix/component';
import { FolderGit2, Settings2 } from 'lucide';
import { projectHasRepo, type MergeStrategy, type Project, type ProjectMode } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { CheckSettingsPanel, DevSettingsPanel } from './project-dev.tsx';
import { MODES, STRATEGIES } from './project.ts';
import { AttachRepository, BriefPanel } from './project-repo.tsx';
import { SettingRow, SettingsGroup, errorPad, footerCss } from './settings-kit.tsx';

const colCss = css({ display: 'grid', gap: t.s8, width: '100%', maxWidth: '820px', margin: '0 auto' });
const valueCss = css({ fontSize: t.textMd, overflowWrap: 'anywhere', textAlign: 'right' });

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
            <select id={modeId} aria-label="Mode" value={mode} disabled={noRepo} mix={on('change', (e) => ((mode = e.currentTarget.value), (saved = false), void handle.update()))}>
              {MODES.map(([v, label]) => (
                <option key={v} value={v} selected={v === mode}>
                  {label}
                </option>
              ))}
            </select>
          </SettingRow>
          <SettingRow label="Merge strategy" for={stratId} description="How the task branch is combined with the default branch.">
            <select id={stratId} aria-label="Merge strategy" value={strategy} disabled={noRepo} mix={on('change', (e) => ((strategy = e.currentTarget.value), (saved = false), void handle.update()))}>
              {STRATEGIES.map(([v, label]) => (
                <option key={v} value={v} selected={v === strategy}>
                  {label}
                </option>
              ))}
            </select>
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
        </div>
      </Page>
    );
  };
}
