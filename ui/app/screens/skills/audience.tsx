// Who gets a skill, changed on the Skills page: the whole team (every agent whose role the skill names, or every agent when it
// names none, agents added later included) plus agents given it on top, or only the agents chosen. Saving sets both at once
// (skill.audience). Switching to chosen agents starts from the agents that get it now, so nobody loses it unless unticked.

import { css, on, type Handle } from 'remix/component';
import type { Skill } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { Button } from '../../ui/button.tsx';
import { Checkbox, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { inWords, reaches, rolesMatch } from './skills.ts';

const panel = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& .choice': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, cursor: 'pointer' },
  '& .choice input': { margin: '0' },
  '& fieldset': { display: 'grid', gap: t.s1, margin: '0', padding: '0', border: '0', minWidth: '0' },
  '& legend': { fontSize: t.textSm, color: t.textMuted, marginBottom: t.s1 },
  '& p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
});

export interface AudienceEditorProps {
  skill: Skill;
  /** The project's team. */
  agents: readonly Agent[];
  busy: boolean;
  onSave: (audience: 'team' | 'agents', agents: string[]) => void;
  onCancel: () => void;
}

export function AudienceEditor(handle: Handle<AudienceEditorProps>) {
  const k0 = handle.props.skill;
  let audience: 'team' | 'agents' = k0.audience === 'agents' ? 'agents' : 'team';
  // the agents given it on top of the team, and the agents chosen when it goes to chosen agents only
  let extra: string[] = [...(k0.agents ?? [])];
  let chosen: string[] = handle.props.agents.filter((a) => reaches(k0, a.id, a.role)).map((a) => a.id);
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const toggle = (list: string[], id: string, on: boolean) => (on ? [...list.filter((x) => x !== id), id] : list.filter((x) => x !== id));

  return () => {
    const { skill: k, agents, busy, onSave, onCancel } = handle.props;
    const roles = k.roles ?? [];
    const radio = `audience-${k.name}`;
    return (
      <div mix={panel} role="group" aria-label={`Who gets ${k.name}`} data-testid="skill-audience">
        <label class="choice">
          <input type="radio" name={radio} checked={audience === 'team'} mix={on('change', () => set(() => (audience = 'team')))} />
          <span>The whole team: {roles.length > 0 ? `every ${inWords(roles, 'or')} agent` : 'every agent'}, agents added later included</span>
        </label>
        <label class="choice">
          <input type="radio" name={radio} checked={audience === 'agents'} mix={on('change', () => set(() => (audience = 'agents')))} />
          <span>Only the agents I choose</span>
        </label>
        <fieldset>
          <legend>{audience === 'team' ? 'Also give it to' : 'Give it to'}</legend>
          {agents.length === 0 ? <p>There is no agent on the team yet.</p> : null}
          {agents.map((a) => {
            const byRole = audience === 'team' && rolesMatch(roles, a.role);
            const list = audience === 'team' ? extra : chosen;
            return (
              <Checkbox
                key={a.id}
                label={a.name}
                description={byRole ? `${a.role}: gets it with the team` : a.role}
                checked={byRole || list.includes(a.id)}
                disabled={byRole}
                onChange={(on) => set(() => (audience === 'team' ? (extra = toggle(extra, a.id, on)) : (chosen = toggle(chosen, a.id, on))))}
              />
            );
          })}
        </fieldset>
        {audience === 'agents' && chosen.length === 0 ? <p>No agent is chosen: nobody will get it.</p> : null}
        <div mix={formActions}>
          <Button size="sm" variant="primary" disabled={busy} mix={on('click', () => onSave(audience, audience === 'team' ? extra : chosen))}>
            Save who gets {k.name}
          </Button>
          <Button size="sm" disabled={busy} mix={on('click', () => onCancel())}>
            Cancel
          </Button>
        </div>
      </div>
    );
  };
}
