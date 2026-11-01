// The New agent dialog's content (decisions P137, P138): three tabs. Describe (the default): one sentence, "Draft agent" (or a
// starter chip) asks the core for a drafted agent (agent.draft: a model writes it, or the offline rules when none is reachable),
// shown as fields the person changes before Create agent. Configure: the full form (AgentForm). From a template: the saved
// templates and the prebuilt gallery.

import { css, on, type Handle } from 'remix/component';
import { Sparkles, UserPlus, X } from 'lucide';
import type { AgentDraft } from '../../api/libraryClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { confirmationFor, needsConfirmation, skipsApprovals, splitConfirmation } from '../../lib/permission.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Select } from '../../ui/select.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t } from '../../ui/tokens.ts';
import { ModelField } from '../shared/model-field.tsx';
import { SkipApprovals } from '../shared/skip-approvals.tsx';
import { AddPrebuilt } from './add-prebuilt.tsx';
import { effortList, modeList, modelList, permissionWords } from './agent-edit.ts';
import { STARTERS, draftSourceNote, draftSummary, specFromDraft } from './agent-draft.ts';
import { AgentForm, FromTemplate } from './agent-forms.tsx';
import { adapterName } from './team.ts';

export type NewAgentTab = 'describe' | 'configure' | 'template';

const panelCss = css({ paddingTop: t.s4 });
const dividerCss = css({ height: '1px', background: t.border });

const describeCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1.2fr)',
  gap: t.s5,
  alignItems: 'start',
  '@media (max-width: 760px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
  '& h3': { margin: '0 0 8px', fontSize: t.textLg, fontWeight: '600' },
  '& textarea': { width: '100%' },
  '& .ask': { display: 'grid', gap: t.s3, alignContent: 'start' },
  '& .starters': {
    display: 'flex',
    flexWrap: 'wrap',
    gap: '6px',
    alignItems: 'center',
    fontSize: t.textSm,
    color: t.textFaint,
  },
  '& .starter': {
    height: '26px',
    minHeight: '0',
    padding: '0 10px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: t.bgSurface,
    color: t.text,
    fontSize: t.textSm,
  },
  '& .starter:hover': { background: t.bgHover },
  '& .note': { margin: '0', fontSize: t.textSm, color: t.textFaint },
});

const draftCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& .head': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s3,
    minWidth: '0',
  },
  '& .head .who': { display: 'grid', gap: '2px', minWidth: '0', flex: '1' },
  '& .head input': { fontSize: t.textLg, fontWeight: '600' },
  '& .head small': { color: t.textMuted, fontSize: t.textSm },
  '& .grid': {
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))',
    gap: t.s3,
  },
  '& .chips': {
    display: 'flex',
    flexWrap: 'wrap',
    gap: '6px',
    alignItems: 'center',
  },
  '& .chip': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '26px',
    padding: '0 4px 0 10px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: t.bgApp,
    fontSize: t.textSm,
  },
  '& .chip button': {
    display: 'inline-grid',
    placeItems: 'center',
    width: '20px',
    height: '20px',
    minHeight: '0',
    padding: '0',
    border: '0',
    borderRadius: t.rFull,
    background: 'transparent',
    color: t.textMuted,
  },
  '& .lbl': {
    margin: '0 0 6px',
    fontSize: t.textSm,
    fontWeight: '500',
    color: t.textMuted,
  },
  '& .why': {
    display: 'flex',
    gap: t.s2,
    margin: '0',
    padding: `${t.s2} ${t.s3}`,
    borderRadius: t.rMd,
    background: t.accentSoft,
    color: t.accentText,
    fontSize: t.textSm,
  },
  '& .acts': {
    display: 'flex',
    flexWrap: 'wrap',
    gap: t.s2,
    justifyContent: 'flex-end',
  },
});

/**
 * Describe: a sentence becomes a drafted agent the person edits, then creates. With `onDraft` (the start screen, decision P142:
 * the project does not exist yet) the button is "Add to crew" and hands the draft, with the mode its box was ticked for, back
 * instead of creating the agent.
 */
export function DescribeAgent(
  handle: Handle<{
    projectId: string;
    adapters: AdapterInfo[];
    onCreated: () => void;
    onDraft?: (d: AgentDraft, confirmedMode: string) => void;
  }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let text = '';
  let draft: AgentDraft | undefined;
  let confirmedMode = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const edit = (p: Partial<AgentDraft>) => set(() => (draft = draft ? { ...draft, ...p } : draft));

  const ask = async (description: string) => {
    const d = await act.run('draft', () => s.client.agentDraft(description.trim(), handle.props.projectId || undefined));
    if (d) set(() => ((draft = d), (confirmedMode = '')));
  };

  const create = async () => {
    const d = draft;
    if (!d) return;
    if (handle.props.onDraft) {
      handle.props.onDraft(d, confirmedMode);
      set(() => ((draft = undefined), (text = '')));
      return;
    }
    const projectId = handle.props.projectId;
    const ok = await act.run('create', async () => {
      const spec = {
        ...specFromDraft(d, projectId),
        ...confirmationFor(d.permissionMode, confirmedMode),
      };
      const ag = await s.client.agentCreate(spec);
      for (const tool of d.tools) await s.client.mcpServerGrant(projectId, tool, ag.id, true);
      return true;
    });
    if (!ok) return;
    set(() => ((draft = undefined), (text = '')));
    handle.props.onCreated();
  };

  return () => {
    const { adapters } = handle.props;
    const ro = s.workspace.license;
    const busy = !!act.busy;
    const { confirm: refusal, other: failure } = splitConfirmation(act.error);
    const d = draft;
    const caps = d ? adapters.find((a) => a.id === d.adapter)?.capabilities : undefined;
    const models = d ? modelList(d.adapter, caps) : [];
    const efforts = d ? effortList(d.adapter, caps) : [];
    const modes = d ? modeList(d.adapter, caps) : [];
    const adapterIds = d ? [...new Set([d.adapter, ...adapters.map((a) => a.id)])] : [];
    return (
      <div mix={describeCss} data-testid="describe-agent">
        <form
          class="ask"
          aria-label="Describe the agent"
          mix={on('submit', (e) => {
            e.preventDefault();
            if (text.trim()) void ask(text);
          })}
        >
          <h3>What should this agent do?</h3>
          <textarea
            aria-label="What should this agent do?"
            data-autofocus
            rows={5}
            maxLength={2000}
            value={text}
            placeholder="Reviews pull requests for security problems. Should never edit code, just comment. Use the best model."
            mix={on('input', (e) => set(() => (text = e.currentTarget.value)))}
          />
          <div class="starters">
            <Button type="submit" variant="primary" icon={Sparkles} disabled={busy || !text.trim()}>
              {act.busy === 'draft' ? 'Drafting...' : 'Draft agent'}
            </Button>
            <span>or start from:</span>
          </div>
          <div class="starters" role="group" aria-label="Start from">
            {STARTERS.map((x) => (
              <button key={x.label} type="button" class="starter" disabled={busy} mix={on('click', () => (set(() => (text = x.text)), void ask(x.text)))}>
                {x.label}
              </button>
            ))}
          </div>
          <p class="note" data-testid="draft-source">
            {draftSourceNote(d)}
          </p>
        </form>
        <div style={{ display: 'grid', gap: '12px', minWidth: '0' }}>
          <ErrorNote error={failure} what={act.busy === 'create' || d ? 'Agent not created' : 'Could not draft the agent'} />
          <ReadOnlyNote />
          {d ? (
            <section mix={draftCss} aria-label="Drafted agent" data-testid="agent-draft" data-source={d.source}>
              <div class="head">
                <Avatar id={d.name || 'draft'} name={d.name || '?'} size={40} kind="agent" />
                <div class="who">
                  <input aria-label="Agent name" value={d.name} autocomplete="off" mix={on('input', (e) => edit({ name: e.currentTarget.value }))} />
                  <small data-testid="draft-summary">{draftSummary(d)}</small>
                </div>
              </div>
              <div class="grid">
                <Field label="Runs on">
                  {(id) => (
                    <Select
                      id={id}
                      label="Runs on"
                      value={d.adapter}
                      options={adapterIds.map((x) => ({
                        value: x,
                        label: adapterName(x),
                      }))}
                      onChange={(v) =>
                        edit({
                          adapter: v,
                          model: '',
                          effort: '',
                          permissionMode: modeList(v, adapters.find((a) => a.id === v)?.capabilities).includes('acceptEdits') ? 'acceptEdits' : '',
                        })
                      }
                    />
                  )}
                </Field>
                <Field label="Model">
                  {(id) => (
                    <ModelField
                      id={id}
                      value={d.model}
                      models={models}
                      emptyLabel="default model"
                      placeholder="default model"
                      onChange={(v) => edit({ model: v })}
                    />
                  )}
                </Field>
                <Field label="Effort">
                  {(id) =>
                    efforts.length > 0 ? (
                      <Select
                        id={id}
                        label="Effort"
                        value={d.effort}
                        options={[{ value: '', label: 'default' }, ...efforts.map((e) => ({ value: e, label: e }))]}
                        onChange={(v) => edit({ effort: v })}
                      />
                    ) : (
                      <input
                        id={id}
                        value={d.effort}
                        placeholder="default"
                        autocomplete="off"
                        mix={on('input', (e) => edit({ effort: e.currentTarget.value }))}
                      />
                    )
                  }
                </Field>
                <Field label="Permissions">
                  {(id) =>
                    modes.length > 0 ? (
                      <Select
                        id={id}
                        label="Permissions"
                        value={d.permissionMode}
                        options={[
                          ...(modes.includes(d.permissionMode)
                            ? []
                            : [
                                {
                                  value: d.permissionMode,
                                  label: permissionWords(d.permissionMode),
                                },
                              ]),
                          ...modes.map((m) => ({
                            value: m,
                            label: permissionWords(m) === m ? m : permissionWords(m),
                            hint: skipsApprovals(m) ? `${m}: skips approvals` : m,
                          })),
                        ]}
                        onChange={(v) => edit({ permissionMode: v })}
                      />
                    ) : (
                      <input id={id} value={permissionWords(d.permissionMode)} readOnly />
                    )
                  }
                </Field>
              </div>
              <SkipApprovals
                mode={d.permissionMode}
                confirmed={confirmedMode}
                onConfirm={(m) => set(() => (confirmedMode = m))}
                who={d.name || 'this agent'}
                error={refusal}
              />
              {d.skills.length + d.tools.length > 0 ? (
                <div>
                  <p class="lbl">Skills and tools</p>
                  <div class="chips">
                    {d.skills.map((k) => (
                      <span key={`s:${k}`} class="chip" data-testid="draft-skill">
                        {k}
                        <button type="button" aria-label={`Leave out ${k}`} mix={on('click', () => edit({ skills: d.skills.filter((x) => x !== k) }))}>
                          <Icon icon={X} size={12} />
                        </button>
                      </span>
                    ))}
                    {d.tools.map((k) => (
                      <span key={`t:${k}`} class="chip" data-testid="draft-tool">
                        {k}
                        <button type="button" aria-label={`Leave out ${k}`} mix={on('click', () => edit({ tools: d.tools.filter((x) => x !== k) }))}>
                          <Icon icon={X} size={12} />
                        </button>
                      </span>
                    ))}
                  </div>
                </div>
              ) : null}
              <Field label="Instructions">
                {(id) => <textarea id={id} rows={5} maxLength={4000} value={d.purpose} mix={on('input', (e) => edit({ purpose: e.currentTarget.value }))} />}
              </Field>
              {d.why ? (
                <p class="why" data-testid="draft-why">
                  <Icon icon={Sparkles} size={14} />
                  <span>{d.why}</span>
                </p>
              ) : null}
              <div class="acts">
                <Button variant="ghost" disabled={busy || !text.trim()} mix={on('click', () => void ask(text))}>
                  Redo draft
                </Button>
                <Button
                  variant="primary"
                  icon={UserPlus}
                  disabled={
                    busy || ro.readOnly || !d.name.trim() || !d.role.trim() || (!handle.props.projectId && !handle.props.onDraft) || needsConfirmation(d.permissionMode, confirmedMode)
                  }
                  title={ro.readOnly ? ro.reason : undefined}
                  mix={on('click', () => void create())}
                >
                  {handle.props.onDraft ? 'Add to crew' : act.busy === 'create' ? 'Creating...' : 'Create agent'}
                </Button>
              </div>
            </section>
          ) : null}
        </div>
      </div>
    );
  };
}

/** The New agent dialog's tabs. `tab` is the one it opens on (Describe unless the caller says). */
export function NewAgent(
  handle: Handle<{
    projectId: string;
    adapters: AdapterInfo[];
    tab?: NewAgentTab;
    onCreated: () => void;
    onAdded?: () => void;
  }>,
) {
  let tab: NewAgentTab = handle.props.tab ?? 'describe';
  return () => {
    const { projectId, adapters, onCreated, onAdded } = handle.props;
    return (
      <Tabs
        label="How to make the agent"
        active={tab}
        onChange={(n) => ((tab = n as NewAgentTab), void handle.update())}
        items={[
          { name: 'describe', label: 'Describe', testId: 'new-agent-describe' },
          {
            name: 'configure',
            label: 'Configure',
            testId: 'new-agent-configure',
          },
          {
            name: 'template',
            label: 'From a template',
            testId: 'new-agent-template',
          },
        ]}
        panelMix={panelCss}
      >
        {(name) =>
          name === 'describe' ? (
            <DescribeAgent key={`d:${projectId}`} projectId={projectId} adapters={adapters} onCreated={onCreated} />
          ) : name === 'configure' ? (
            <AgentForm key={`c:${projectId}`} projectId={projectId} adapters={adapters} onCreated={onCreated} />
          ) : (
            <div style={{ display: 'grid', gap: '16px' }} data-testid="crew-templates">
              <FromTemplate key={`t:${projectId}`} projectId={projectId} onCreated={onAdded ?? onCreated} />
              <div mix={dividerCss} role="presentation" />
              <AddPrebuilt key={`p:${projectId}`} projectId={projectId} onAdded={() => (onAdded ?? onCreated)()} />
            </div>
          )
        }
      </Tabs>
    );
  };
}
