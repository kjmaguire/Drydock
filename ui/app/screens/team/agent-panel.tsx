// The agent editor panel (decision P137, the owner's "we can't change the agents once they are in a project"): a panel at the
// right edge that edits an agent in place. Runs on (fixed: a different CLI is a new agent), Model, Effort, Permissions, Skills
// and tools, Instructions, and "This project | Everywhere" (Everywhere also saves the change to the agent's template and to the
// other agents made from it, or saves it as a template when it has none). Name, role, where it runs, the Claude best practices,
// model routing, Save as a template and Remove are under "More settings". Model and effort are never disabled: while the CLI's
// capabilities load, or when they cannot be read, the panel offers what it knows (agent-edit.ts KNOWN_CAPS) or takes typed text.
//
// Any page opens it with openAgentEditor(agentId) as long as it renders one <AgentPanelHost /> (the Crew page and an agent's
// page do); /team?edit=<agentId> opens it too.

import { css, on, type Handle } from 'remix/component';
import { BookmarkPlus, Trash2 } from 'lucide';
import type { AdapterInfo, Agent, AgentUpdate, RouteModel } from '../../api/types.ts';
import { confirmationFor, isNeedsConfirmation, skipsApprovals, splitConfirmation } from '../../lib/permission.ts';
import { routes as appRoutes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Segmented } from '../../ui/segmented.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ModelField } from '../shared/model-field.tsx';
import { SkipApprovals } from '../shared/skip-approvals.tsx';
import { noSkillChanges, type SkillChanges } from '../skills/skills.ts';
import {
  editsOf,
  effortList,
  effortSegments,
  everywhereNote,
  modeList,
  modelChip,
  modelList,
  permissionChoices,
  permissionWords,
  scopeNote,
  updateFor,
  type AgentEdits,
} from './agent-edit.ts';
import { ClaudePracticesPanel, RouteModelsField, claudePractices } from './fields.tsx';
import { RunModeField } from './run-mode-field.tsx';
import { supportsWSL } from './run-mode.ts';
import { SkillToolChips, type ToolChanges } from './skill-chips.tsx';
import { PURPOSE_HELP, adapterName, routesForUpdate, runsWhere } from './team.ts';

// ---- the opener: a tiny store the host listens to ----

let wanted = '';
const listeners = new Set<() => void>();

/** Opens the agent editor panel for this agent on the page's <AgentPanelHost />. */
export function openAgentEditor(agentId: string): void {
  wanted = agentId;
  for (const f of listeners) f();
}

/** Closes the panel. */
export function closeAgentEditor(): void {
  openAgentEditor('');
}

/** The agent the panel is open for ('' when closed): for tests. */
export const agentEditorOpenFor = (): string => wanted;

// ---- the form ----

const formCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: t.s4,
  minHeight: '100%',
  '& .row': {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) auto',
    gap: t.s3,
    alignItems: 'start',
  },
  '@media (max-width: 520px)': {
    '& .row': { gridTemplateColumns: 'minmax(0, 1fr)' },
  },
  '& .lbl': {
    fontSize: t.textSm,
    fontWeight: '500',
    color: t.textMuted,
    margin: '0 0 6px',
  },
  '& .ro': {
    display: 'flex',
    alignItems: 'center',
    minHeight: '32px',
    padding: `0 ${t.s3}`,
    border: `1px solid ${t.border}`,
    borderRadius: t.rMd,
    background: t.bgSurface,
    color: t.text,
    fontSize: t.textMd,
  },
  '& .hint': { margin: '6px 0 0', fontSize: t.textSm, color: t.textFaint },
  '& .scope': {
    display: 'grid',
    gap: t.s2,
    padding: t.s3,
    borderRadius: t.rLg,
    background: t.bgSurface,
    border: `1px solid ${t.border}`,
  },
  '& .scope p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
  '& .more': { display: 'grid', gap: t.s4 },
  '& textarea': { width: '100%' },
});

const footCss = css({
  position: 'sticky',
  bottom: `calc(-1 * ${t.s5})`,
  marginTop: 'auto',
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s2,
  padding: `${t.s3} 0 ${t.s5}`,
  background: t.bgOverlay,
  borderTop: `1px solid ${t.border}`,
  '& .when': { marginRight: 'auto', fontSize: t.textSm, color: t.textFaint },
});

const titleCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  minWidth: '0',
  '& .who': { display: 'grid', minWidth: '0' },
  '& .who b': {
    fontSize: t.textLg,
    fontWeight: '600',
    whiteSpace: 'nowrap',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
  },
  '& .who small': { fontSize: t.textSm, fontWeight: '400', color: t.textMuted },
});

const removeRow = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s2,
  fontSize: t.textMd,
});
const dangerBtn = css({
  color: t.danger,
  '&:hover': { color: t.danger, background: t.dangerSoft },
});

export type CapsState = 'loading' | 'ready' | 'failed';

export interface AgentPanelProps {
  agent: Agent;
  adapter: AdapterInfo | undefined;
  /** Whether the CLI's capabilities are still being read, were read, or could not be. */
  caps: CapsState;
  projectName?: string;
  /** After a save (with what an "Everywhere" save did, in words) or a removal. */
  onDone: (note: string) => void;
  onCancel: () => void;
}

/** The editor form inside the panel. */
export function AgentEditor(handle: Handle<AgentPanelProps>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const a0 = handle.props.agent;
  let edits: AgentEdits = editsOf(a0);
  let everywhere = false;
  let confirmedMode = '';
  let practices = a0.practices !== false;
  let routes: RouteModel[] = a0.routeModels ?? [];
  let runMode = a0.runMode ?? '';
  let wslDistro = a0.wslDistro ?? '';
  let skills: SkillChanges = noSkillChanges();
  let tools: ToolChanges = noSkillChanges();
  let confirmRemove = false;
  let savedAs = '';
  // Save as a template refused for a mode that skips approvals: that is confirmed in the Library, not here
  let templateRefusal: Error | undefined;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const edit = (p: Partial<AgentEdits>) => set(() => (edits = { ...edits, ...p }));
  // an existing agent keeps its model and effort: the Claude recommendation is shown, not applied over them
  const pr = claudePractices(handle, {
    adapter: () => handle.props.agent.adapter,
    role: () => edits.role,
  });

  const save = async () => {
    templateRefusal = undefined;
    const agent = handle.props.agent;
    const u: AgentUpdate = updateFor(agent, edits);
    if (u.permissionMode !== undefined) Object.assign(u, confirmationFor(u.permissionMode, confirmedMode));
    if (agent.adapter === 'claude' && practices !== (agent.practices !== false)) u.practices = practices;
    const rm = routesForUpdate(routes, agent.routeModels);
    if (rm) u.routeModels = rm;
    if (skills.add.length > 0) u.addSkills = skills.add;
    if (skills.remove.length > 0) u.removeSkills = skills.remove;
    if (supportsWSL(handle.props.adapter?.capabilities) && (runMode !== (agent.runMode ?? '') || wslDistro !== (agent.wslDistro ?? ''))) {
      u.runMode = runMode;
      u.wslDistro = runMode === 'wsl' ? wslDistro : '';
    }
    if (everywhere) u.everywhere = true;
    const note = await act.run('save', async () => {
      const r = await s.client.agentUpdate(u);
      for (const id of tools.add) await s.client.mcpServerGrant(agent.projectId, id, agent.id, true);
      for (const id of tools.remove) await s.client.mcpServerGrant(agent.projectId, id, agent.id, false);
      return r.everywhere
        ? everywhereNote({
            ...r.everywhere,
            skipped: r.everywhere.skipped ?? [],
          })
        : 'Saved.';
    });
    if (note !== undefined) handle.props.onDone(note);
  };

  const saveTemplate = async () => {
    templateRefusal = undefined;
    const tpl = await act.run('template', () => s.client.agentTemplateFromAgent(handle.props.agent.id));
    if (tpl) set(() => (savedAs = tpl.name));
    else if (isNeedsConfirmation(act.error)) set(() => (templateRefusal = act.error));
  };

  const remove = async () => {
    const ok = await act.run('remove', async () => {
      await s.client.agentArchive(handle.props.agent.id);
      return true;
    });
    if (ok) handle.props.onDone('');
  };

  return () => {
    const { agent, adapter, caps: capsState, projectName = '', onCancel } = handle.props;
    const caps = adapter?.capabilities;
    const ro = s.workspace.license;
    const busy = !!act.busy;
    const models = modelList(agent.adapter, caps);
    const efforts = effortList(agent.adapter, caps);
    const modes = modeList(agent.adapter, caps);
    const perm = permissionChoices(modes, edits.permissionMode);
    const changedMode = edits.permissionMode !== (agent.permissionMode ?? '');
    const needsBox = changedMode && skipsApprovals(edits.permissionMode) && confirmedMode !== edits.permissionMode;
    const { confirm: refusal, other: failure } = splitConfirmation(act.error);
    const where = runsWhere(agent);
    const chip = modelChip(edits);
    return (
      <form mix={[formCss, on('submit', (e) => (e.preventDefault(), void save()))]} aria-label={`Edit ${agent.name}`} data-testid="agent-editor">
        <ErrorNote error={failure} what="Agent not changed" />
        <ReadOnlyNote />
        {capsState === 'loading' ? (
          <p mix={muted} style={{ margin: '0', fontSize: 'var(--text-sm)' }} data-testid="caps-loading">
            Reading {adapterName(agent.adapter)}'s choices...
          </p>
        ) : capsState === 'failed' ? (
          <p mix={muted} style={{ margin: '0', fontSize: 'var(--text-sm)' }} data-testid="caps-failed">
            Could not read {adapterName(agent.adapter)}'s choices: pick from the known ones or type a model.
          </p>
        ) : null}

        <div>
          <p class="lbl">Runs on</p>
          <div class="ro" data-testid="agent-runs-on" title="A different CLI is a new agent">
            {adapterName(agent.adapter)}
            {where ? ` · ${where}` : ''}
          </div>
        </div>

        <div class="row">
          <Field label="Model">
            {(id) => (
              <ModelField
                id={id}
                value={edits.model}
                models={models}
                emptyLabel="default model"
                placeholder="default model"
                onChange={(v) => edit({ model: v })}
              />
            )}
          </Field>
          {efforts.length > 0 ? (
            <div>
              <p class="lbl">Effort</p>
              <Segmented
                label="Effort"
                value={edits.effort}
                options={effortSegments(efforts, edits.effort)}
                onChange={(v) => edit({ effort: v })}
                testId="effort"
              />
            </div>
          ) : (
            <Field label="Effort">
              {(id) => (
                <input
                  id={id}
                  value={edits.effort}
                  spellcheck={false}
                  autocomplete="off"
                  placeholder="default"
                  mix={on('input', (e) => edit({ effort: e.currentTarget.value }))}
                />
              )}
            </Field>
          )}
        </div>

        <div>
          <p class="lbl">Permissions</p>
          {perm.segments.length > 0 || perm.others.length > 0 ? (
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: '8px',
                alignItems: 'center',
              }}
            >
              {perm.segments.length > 0 ? (
                <Segmented
                  label="Permissions"
                  value={edits.permissionMode}
                  options={perm.segments}
                  onChange={(v) => edit({ permissionMode: v })}
                  testId="permissions"
                />
              ) : null}
              {perm.others.length > 0 ? (
                <Select
                  label="Other permission mode"
                  value={perm.others.includes(edits.permissionMode) ? edits.permissionMode : ''}
                  placeholder="Other..."
                  options={perm.others.map((m) => ({
                    value: m,
                    label: m,
                    hint: skipsApprovals(m) ? 'skips approvals' : undefined,
                  }))}
                  onChange={(v) => edit({ permissionMode: v })}
                  testId="permission-other"
                />
              ) : null}
            </div>
          ) : (
            <div class="ro">{permissionWords(edits.permissionMode)}</div>
          )}
        </div>
        <SkipApprovals
          mode={changedMode ? edits.permissionMode : ''}
          confirmed={confirmedMode}
          onConfirm={(m) => set(() => (confirmedMode = m))}
          who={agent.name}
          error={templateRefusal ? undefined : refusal}
        />

        <div>
          <p class="lbl">Skills and tools</p>
          <SkillToolChips
            projectId={agent.projectId}
            agentId={agent.id}
            skills={skills}
            onSkills={(c) => set(() => (skills = c))}
            tools={tools}
            onTools={(c) => set(() => (tools = c))}
          />
        </div>

        <Field label="Instructions">
          {(id) => (
            <textarea
              id={id}
              value={edits.purpose}
              rows={5}
              maxLength={4000}
              placeholder={PURPOSE_HELP}
              mix={on('input', (e) => edit({ purpose: e.currentTarget.value }))}
            />
          )}
        </Field>

        <div class="scope">
          <Segmented
            label="Save for"
            value={everywhere ? 'everywhere' : 'project'}
            options={[
              { value: 'project', label: 'This project' },
              { value: 'everywhere', label: 'Everywhere' },
            ]}
            onChange={(v) => set(() => (everywhere = v === 'everywhere'))}
            testId="save-scope"
          />
          <p data-testid="scope-note">{scopeNote(agent, everywhere, projectName, chip)}</p>
        </div>

        <Disclosure summary="More settings" testId="agent-more-settings">
          <div class="more">
            <Field label="Agent name">
              {(id) => <input id={id} value={edits.name} required autocomplete="off" mix={on('input', (e) => edit({ name: e.currentTarget.value }))} />}
            </Field>
            <Field label="Role">
              {(id) => <input id={id} value={edits.role} required autocomplete="off" mix={on('input', (e) => edit({ role: e.currentTarget.value }))} />}
            </Field>
            <RunModeField caps={caps} runMode={runMode} wslDistro={wslDistro} onChange={(m, d) => set(() => ((runMode = m), (wslDistro = d)))} />
            <ClaudePracticesPanel info={pr.info} on={practices} onChange={(v) => set(() => (practices = v))} />
            <RouteModelsField routes={routes} onRoutes={(r) => set(() => (routes = r))} suggestions={models} />
            {savedAs ? (
              <Notice tone="ok" role="status" testId="saved-as-template">
                Saved as the template {savedAs}. Find it in the <a href={appRoutes.library.href()}>Library</a>.
              </Notice>
            ) : null}
            {templateRefusal ? (
              <Notice tone="warn" role="alert" testId="template-needs-confirmation" title="This agent cannot be saved as a template from here.">
                {templateRefusal.message} Make the template in the <a href={appRoutes.library.href()}>Library</a>, where you can confirm the permission mode.
              </Notice>
            ) : null}
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px' }}>
              <Button variant="ghost" icon={BookmarkPlus} disabled={busy} mix={on('click', () => void saveTemplate())}>
                Save as a template
              </Button>
              {confirmRemove ? null : (
                <Button variant="ghost" icon={Trash2} disabled={busy} mix={[on('click', () => set(() => (confirmRemove = true))), dangerBtn]}>
                  Remove from team
                </Button>
              )}
            </div>
            {confirmRemove ? (
              <div mix={removeRow} role="group" aria-label="Confirm removal">
                <span>Remove {agent.name} from the team? Its history stays.</span>
                <Button variant="danger" disabled={busy} mix={on('click', () => void remove())}>
                  {act.busy === 'remove' ? 'Removing...' : 'Yes, remove'}
                </Button>
                <Button disabled={busy} mix={on('click', () => set(() => (confirmRemove = false)))}>
                  Keep
                </Button>
              </div>
            ) : null}
          </div>
        </Disclosure>

        <div mix={footCss}>
          <span class="when">Applies from its next turn</span>
          <Button disabled={busy} mix={on('click', () => onCancel())}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={busy || ro.readOnly || !edits.name.trim() || !edits.role.trim() || needsBox}
            title={ro.readOnly ? ro.reason : needsBox ? 'Tick the box above first' : undefined}
          >
            {act.busy === 'save' ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </form>
    );
  };
}

/** The panel's title: the agent and where it is. */
function PanelTitle(handle: Handle<{ agent: Agent; projectName: string }>) {
  return () => {
    const { agent, projectName } = handle.props;
    return (
      <span mix={titleCss}>
        <Avatar id={agent.id} name={agent.name} size={32} kind="agent" />
        <span class="who">
          <b>{agent.name}</b>
          {projectName ? <small>in {projectName}</small> : null}
        </span>
      </span>
    );
  };
}

/**
 * The panel itself, opened by openAgentEditor(agentId). `agents` are the agents the page has (the panel reads one it does not
 * have from the core); `onSaved` hears the note of each save.
 */
export function AgentPanelHost(
  handle: Handle<{
    agents?: readonly Agent[];
    onSaved?: (agent: Agent | undefined, note: string) => void;
  }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  let open = wanted;
  const sync = () => {
    if (open === wanted) return;
    open = wanted;
    void handle.update();
  };
  listeners.add(sync);
  handle.signal.addEventListener('abort', () => {
    listeners.delete(sync);
  });
  const known = () => (handle.props.agents ?? s.project.agentList).find((a) => a.id === open);
  const fetched = query(handle, {
    load: async () => (await s.client.agentGet(open)).agent,
    key: () => open,
    enabled: () => !!open && !known(),
  });
  const adapters = query(handle, {
    load: () => s.client.adapterList(),
    enabled: () => !!open,
  });

  return () => {
    const agent = open ? (known() ?? (fetched.data?.id === open ? fetched.data : undefined)) : undefined;
    const projectName = agent ? (s.workspace.projectList.find((p) => p.id === agent.projectId)?.name ?? '') : '';
    const close = () => closeAgentEditor();
    const capsState: CapsState = adapters.data ? 'ready' : adapters.error ? 'failed' : 'loading';
    return (
      <Dialog
        open={!!agent}
        side
        title={agent ? <PanelTitle agent={agent} projectName={projectName} /> : ''}
        label={agent ? `Edit ${agent.name}` : 'Edit agent'}
        testId="agent-panel"
        onClose={close}
      >
        {agent ? (
          <AgentEditor
            key={agent.id}
            agent={agent}
            adapter={(adapters.data ?? []).find((x) => x.id === agent.adapter)}
            caps={capsState}
            projectName={projectName}
            onDone={(n) => {
              void s.project.agents.reload();
              handle.props.onSaved?.(agent, n);
              close();
            }}
            onCancel={close}
          />
        ) : null}
      </Dialog>
    );
  };
}
