// Who gets a skill, changed on the Skills page: the agents it is given to (decision P76: each agent is a specialist in its role,
// never the whole team). An agent whose role the skill names is marked as suggested; ticking gives the skill, unticking takes
// it away. Saving sets the list at once (skill.audience).

import { css, on, type Handle } from 'remix/component';
import type { Skill } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { Button } from '../../ui/button.tsx';
import { Checkbox, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { reaches, suggestsRole } from './skills.ts';

const panel = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& fieldset': { display: 'grid', gap: t.s1, margin: '0', padding: '0', border: '0', minWidth: '0' },
  '& legend': { fontSize: t.textSm, color: t.textMuted, marginBottom: t.s1 },
  '& p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
});

export interface AudienceEditorProps {
  skill: Skill;
  /** The project's team. */
  agents: readonly Agent[];
  busy: boolean;
  onSave: (agents: string[]) => void;
  onCancel: () => void;
}

export function AudienceEditor(handle: Handle<AudienceEditorProps>) {
  const k0 = handle.props.skill;
  let chosen: string[] = handle.props.agents.filter((a) => reaches(k0, a.id)).map((a) => a.id);
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const toggle = (id: string, on: boolean) => (chosen = on ? [...chosen.filter((x) => x !== id), id] : chosen.filter((x) => x !== id));

  return () => {
    const { skill: k, agents, busy, onSave, onCancel } = handle.props;
    return (
      <div mix={panel} role="group" aria-label={`Who gets ${k.name}`} data-testid="skill-audience">
        <fieldset>
          <legend>Give it to</legend>
          {agents.length === 0 ? <p>There is no agent on the team yet.</p> : null}
          {agents.map((a) => (
            <Checkbox
              key={a.id}
              label={a.name}
              description={suggestsRole(k, a.role) ? `${a.role}: the skill names this role` : a.role}
              checked={chosen.includes(a.id)}
              onChange={(v) => set(() => toggle(a.id, v))}
            />
          ))}
        </fieldset>
        {chosen.length === 0 ? <p>No agent is chosen: nobody will get it.</p> : null}
        <div mix={formActions}>
          <Button size="sm" variant="primary" disabled={busy} mix={on('click', () => onSave(chosen))}>
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
