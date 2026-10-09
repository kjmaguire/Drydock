// The wizard's Team step: the team being assembled (each row editable: name, agent CLI, model, effort, permission mode),
// the prebuilt agents gallery, a custom agent, an agent from a saved template, and whether to install the skills the
// prebuilt agents come with. Nothing is created here: the wizard sends the list to project.setup.

import { css, on, type Handle } from 'remix/component';
import { BookTemplate, Sparkles, Trash2, UserPlus, Users } from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { AdapterInfo, PrebuiltAgent } from '../../api/types.ts';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge } from '../../ui/data.tsx';
import { Empty } from '../../ui/feedback.tsx';
import { Checkbox, Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import { adapterChoices, memberFromPrebuilt, memberKey, setupFor, uniqueName, type TeamMember } from './wizard.ts';

const blockCss = css({ display: 'grid', gap: t.s3, minWidth: '0' });
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  '& h2': { margin: '0', fontSize: t.textMd, fontWeight: '600' },
  '& .count': { color: t.textFaint, fontSize: t.textSm },
  '& .end': { marginLeft: 'auto' },
});
const listCss = css({ display: 'grid', margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface });
const rowCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderBottom: `1px solid ${t.border}`,
  '&:last-child': { borderBottom: '0' },
  '& fieldset': { margin: '0', padding: '0', border: '0', minWidth: '0' },
  '& legend': { padding: '0', float: 'left', width: '100%' },
  '& .who': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', marginBottom: t.s2 },
  '& .who strong': { fontSize: t.textMd, fontWeight: '600', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .who .end': { marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s2 },
  '& .fields': { clear: 'both', display: 'grid', gridTemplateColumns: 'minmax(130px, 1.2fr) minmax(130px, 1fr) minmax(110px, 1fr) minmax(90px, 0.8fr) minmax(110px, 0.9fr)', gap: t.s2 },
  '& .fields input, & .fields select': { width: '100%', minWidth: '0' },
  '& .fields label': { fontSize: t.textXs },
  '& .note': { margin: '0', color: t.textFaint, fontSize: t.textXs },
});
const addCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& summary': { cursor: 'pointer', fontWeight: '500', display: 'inline-flex', alignItems: 'center', gap: t.s2 },
  '& .grid': { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: t.s3, marginTop: t.s3 },
  '& .grid input, & .grid select, & textarea': { width: '100%' },
  '& .actions': { display: 'flex', justifyContent: 'flex-end', marginTop: t.s3 },
});
const tplCss = css({ display: 'flex', alignItems: 'flex-end', gap: t.s2, flexWrap: 'wrap', '& > div': { flex: '1', minWidth: '200px' }, '& select': { width: '100%' } });

/** The CLI, model, effort and permission inputs of one agent (shared by the rows and the custom agent form). */
function SetupFields(handle: Handle<{ m: TeamMember; entry?: PrebuiltAgent; adapters: AdapterInfo[]; onChange: (m: TeamMember) => void; withName?: boolean }>) {
  const listId = `models-${handle.id}`;
  return () => {
    const { m, entry, adapters, onChange, withName = true } = handle.props;
    const caps = adapters.find((a) => a.id === m.adapter)?.capabilities;
    const models = [...new Set([...(entry && setupFor(entry, m.adapter).model ? [setupFor(entry, m.adapter).model] : []), ...(caps?.models ?? [])])];
    const efforts = caps?.efforts ?? [];
    const perms = caps?.permissionModes ?? [];
    const set = (patch: Partial<TeamMember>) => onChange({ ...m, ...patch });
    const changeAdapter = (adapter: string) => {
      const next = entry ? setupFor(entry, adapter) : { model: '', effort: '' };
      set({ adapter, model: next.model, effort: next.effort, permissionMode: entry && adapter === entry.preferred.adapter ? entry.preferred.permissionMode || '' : '' });
    };
    return (
      <>
        {withName ? (
          <Field label="Name">{(id) => <input id={id} value={m.name} autocomplete="off" spellcheck={false} mix={on('input', (e) => set({ name: e.currentTarget.value }))} />}</Field>
        ) : null}
        <Field label="Agent CLI">
          {(id) => (
            <select id={id} value={m.adapter} mix={on('change', (e) => changeAdapter(e.currentTarget.value))}>
              {m.adapter ? null : (
                <option value="" selected>
                  choose...
                </option>
              )}
              {adapterChoices(adapters, entry, m.adapter).map((c) => (
                <option key={c.id} value={c.id} disabled={c.disabled} selected={c.id === m.adapter} title={c.reason}>
                  {c.label}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Model">
          {(id) => (
            <>
              <input id={id} value={m.model} list={models.length ? listId : undefined} placeholder="CLI default" autocomplete="off" spellcheck={false} mix={on('input', (e) => set({ model: e.currentTarget.value }))} />
              {models.length ? (
                <datalist id={listId}>
                  {models.map((x) => (
                    <option key={x} value={x} />
                  ))}
                </datalist>
              ) : null}
            </>
          )}
        </Field>
        <Field label="Effort">
          {(id) =>
            efforts.length ? (
              <select id={id} value={m.effort} mix={on('change', (e) => set({ effort: e.currentTarget.value }))}>
                <option value="" selected={m.effort === ''}>
                  default
                </option>
                {[...new Set([...efforts, ...(m.effort ? [m.effort] : [])])].map((x) => (
                  <option key={x} value={x} selected={x === m.effort}>
                    {x}
                  </option>
                ))}
              </select>
            ) : (
              <input id={id} value={m.effort} placeholder="default" autocomplete="off" mix={on('input', (e) => set({ effort: e.currentTarget.value }))} />
            )
          }
        </Field>
        <Field label="Permissions">
          {(id) => (
            <select id={id} value={m.permissionMode} mix={on('change', (e) => set({ permissionMode: e.currentTarget.value }))}>
              <option value="" selected={m.permissionMode === ''}>
                default
              </option>
              {[...new Set([...perms, ...(m.permissionMode ? [m.permissionMode] : [])])].map((x) => (
                <option key={x} value={x} selected={x === m.permissionMode}>
                  {x}
                </option>
              ))}
            </select>
          )}
        </Field>
      </>
    );
  };
}

function TeamRow(handle: Handle<{ m: TeamMember; entry?: PrebuiltAgent; adapters: AdapterInfo[]; onChange: (m: TeamMember) => void; onRemove: () => void }>) {
  return () => {
    const { m, entry, adapters, onChange, onRemove } = handle.props;
    const label = m.name.trim() || 'Unnamed agent';
    return (
      <li mix={rowCss} data-testid="team-member" data-source={m.source} data-ref={m.ref}>
        <fieldset>
          <legend>
            <span class="who">
              <Avatar id={m.ref ?? m.key} name={label} size={24} kind="agent" />
              <strong>{label}</strong>
              <Badge tone="muted">{m.role || 'role?'}</Badge>
              <span class="end">
                {m.source === 'prebuilt' ? <Badge tone="accent">prebuilt</Badge> : m.source === 'template' ? <Badge tone="info">template</Badge> : <Badge tone="muted">custom</Badge>}
                {m.skills ? <Badge tone="muted">{`${m.skills} ${m.skills === 1 ? 'skill' : 'skills'}`}</Badge> : null}
                <IconButton icon={Trash2} label={`Remove ${label} from the team`} mix={on('click', onRemove)} />
              </span>
            </span>
          </legend>
          {m.source === 'template' ? (
            <div class="fields" style={{ gridTemplateColumns: 'minmax(160px, 320px)' }}>
              <Field label="Name">{(id) => <input id={id} value={m.name} autocomplete="off" mix={on('input', (e) => onChange({ ...m, name: e.currentTarget.value }))} />}</Field>
            </div>
          ) : (
            <div class="fields">
              <SetupFields m={m} entry={entry} adapters={adapters} onChange={onChange} />
            </div>
          )}
          {m.source === 'template' ? <p class="note">The template sets the agent CLI, model and connectors.</p> : null}
        </fieldset>
      </li>
    );
  };
}

function blankCustom(): TeamMember {
  return { key: memberKey(), source: 'custom', name: '', role: '', adapter: '', model: '', effort: '', permissionMode: '', purpose: '' };
}

export function TeamBuilder(
  handle: Handle<{
    team: TeamMember[];
    catalog: PrebuiltAgent[];
    adapters: AdapterInfo[];
    templates: AgentTemplate[];
    installSkills: boolean;
    ready: boolean;
    onChange: (team: TeamMember[]) => void;
    onInstallSkills: (v: boolean) => void;
  }>,
) {
  let custom = blankCustom();
  let customOpen = false;
  let template = '';

  return () => {
    const { team, catalog, adapters, templates, installSkills, ready, onChange, onInstallSkills } = handle.props;
    const replace = (i: number, m: TeamMember) => onChange(team.map((x, j) => (j === i ? m : x)));
    const remove = (i: number) => onChange(team.filter((_, j) => j !== i));
    const addPrebuilt = (e: PrebuiltAgent) => onChange([...team, memberFromPrebuilt(e, team)]);
    const firstUsable = adapters.find((a) => a.detection.installed)?.id ?? '';
    const c = { ...custom, adapter: custom.adapter || firstUsable };
    const customOk = !!c.name.trim() && !!c.role.trim() && !!c.adapter;
    const addCustom = () => {
      if (!customOk) return;
      onChange([...team, { ...c, name: uniqueName(c.name.trim(), team) }]);
      custom = blankCustom();
      void handle.update();
    };
    const addTemplate = () => {
      const tp = templates.find((x) => x.id === template);
      if (!tp) return;
      onChange([
        ...team,
        { key: memberKey(), source: 'template', ref: tp.id, name: uniqueName(tp.name, team), role: tp.role, adapter: tp.adapter, model: tp.model, effort: tp.effort, permissionMode: tp.permissionMode, purpose: tp.purpose },
      ]);
      template = '';
      void handle.update();
    };
    const prebuiltCount = team.filter((m) => m.source === 'prebuilt' && (m.skills ?? 0) > 0).length;
    return (
      <div style={{ display: 'grid', gap: 'var(--space-6)' }}>
        <div mix={blockCss}>
          <div mix={headCss}>
            <h2>Your team</h2>
            <span class="count">{team.length === 0 ? 'no agents yet' : `${team.length} ${team.length === 1 ? 'agent' : 'agents'}`}</span>
            {team.length > 0 ? (
              <span class="end">
                <Button size="sm" variant="ghost" mix={on('click', () => onChange([]))}>
                  Remove all
                </Button>
              </span>
            ) : null}
          </div>
          {team.length === 0 ? (
            <div data-testid="team-list" data-ready={ready ? 'true' : 'false'}>
              <Empty title="No agents yet" icon={Users}>
                <p>Add prebuilt agents below, or describe your own. You can also add agents later on the Team page.</p>
              </Empty>
            </div>
          ) : (
            <ul mix={listCss} data-testid="team-list" data-ready={ready ? 'true' : 'false'} aria-label="Your team">
              {team.map((m, i) => (
                <TeamRow key={m.key} m={m} entry={m.source === 'prebuilt' ? catalog.find((e) => e.id === m.ref) : undefined} adapters={adapters} onChange={(x) => replace(i, x)} onRemove={() => remove(i)} />
              ))}
            </ul>
          )}
          <Checkbox
            label="Install the skills these agents come with"
            description={
              prebuiltCount > 0
                ? `Prebuilt agents bring pinned skills from public registries; ${prebuiltCount === 1 ? 'one agent has' : `${prebuiltCount} agents have`} some. Drydock checks them before use.`
                : 'Prebuilt agents bring pinned skills from public registries; Drydock checks them before use.'
            }
            checked={installSkills}
            onChange={onInstallSkills}
          />
        </div>

        <div mix={blockCss}>
          <div mix={headCss}>
            <Icon icon={Sparkles} size={14} />
            <h2>Prebuilt agents</h2>
          </div>
          <AgentGallery entries={catalog} onPick={addPrebuilt} label="Prebuilt agents" />
        </div>

        <div mix={addCss}>
          <details open={customOpen} mix={on('toggle', (e) => ((customOpen = (e.currentTarget as HTMLDetailsElement).open), void handle.update()))}>
            <summary>
              <Icon icon={UserPlus} size={14} />
              Add custom agent
            </summary>
            <div class="grid">
              <Field label="Agent name">{(id) => <input id={id} value={c.name} autocomplete="off" mix={on('input', (e) => ((custom = { ...custom, name: e.currentTarget.value }), void handle.update()))} />}</Field>
              <Field label="Role">{(id) => <input id={id} value={c.role} placeholder="backend, reviewer..." autocomplete="off" mix={on('input', (e) => ((custom = { ...custom, role: e.currentTarget.value }), void handle.update()))} />}</Field>
              <SetupFields m={c} adapters={adapters} withName={false} onChange={(m) => ((custom = m), void handle.update())} />
            </div>
            <div style={{ marginTop: 'var(--space-3)' }}>
              <Field label="Purpose (optional)" hint="What this agent is for and how it should work. Given to the agent as standing instructions.">
                {(id) => <textarea id={id} rows={3} value={c.purpose} mix={on('input', (e) => ((custom = { ...custom, purpose: e.currentTarget.value }), void handle.update()))} />}
              </Field>
            </div>
            <div class="actions">
              <Button size="sm" variant="secondary" icon={UserPlus} disabled={!customOk} mix={on('click', addCustom)}>
                Add to team
              </Button>
            </div>
          </details>
        </div>

        {templates.length > 0 ? (
          <div mix={addCss}>
            <div mix={tplCss}>
              <Field label="From template">
                {(id) => (
                  <select id={id} value={template} mix={on('change', (e) => ((template = e.currentTarget.value), void handle.update()))}>
                    <option value="" selected={template === ''}>
                      choose a saved template...
                    </option>
                    {templates.map((tp) => (
                      <option key={tp.id} value={tp.id} selected={tp.id === template}>
                        {tp.name} ({tp.role}, {tp.adapter})
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <Button size="sm" variant="secondary" icon={BookTemplate} disabled={!template} mix={on('click', addTemplate)}>
                Add from template
              </Button>
            </div>
          </div>
        ) : null}
      </div>
    );
  };
}
