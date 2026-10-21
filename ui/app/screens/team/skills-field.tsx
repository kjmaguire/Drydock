// The Skills section of the agent forms: which of the project's accepted skills the agent gets (decision P76: skills are given
// to agents, each a specialist in its role). Ticking a skill gives it to this agent and takes it from nobody; a skill that
// names the agent's role comes first, marked as suggested.

import { css, type Handle } from 'remix/component';
import { Cpu } from 'lucide';
import type { Skill } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { audienceText, isGiven, onTeam, suggestsRole, toggleSkill, type SkillChanges } from '../skills/skills.ts';

const fieldCss = css({
  display: 'grid',
  gap: t.s2,
  gridColumn: '1 / -1',
  minWidth: '0',
  '& legend': { display: 'inline-flex', alignItems: 'center', gap: '6px' },
  '& p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
  '& code': { fontSize: t.textMd },
  '& .desc': { color: t.textMuted, fontSize: t.textSm, marginLeft: t.s2 },
});

/** The first sentence-ish part of a skill's description, for one line next to its name. */
const excerpt = (s: string, max = 110): string => (s.length > max ? `${s.slice(0, max).trimEnd()}...` : s);

export interface SkillsFieldProps {
  projectId: string;
  /** The agent being edited; empty for a new agent. */
  agentId?: string;
  /** The role typed in the form: a skill that names it comes first, marked as suggested. */
  role: string;
  /** The skills ticked or unticked so far; only these change when the form is saved. */
  changes: SkillChanges;
  onChange: (changes: SkillChanges) => void;
}

export function SkillsField(handle: Handle<SkillsFieldProps>) {
  const s = services(handle);
  watch(handle, s.project);
  const q = query(handle, {
    load: () => s.client.skillList(handle.props.projectId),
    when: (e) => e.type.startsWith('skill.'),
    key: () => handle.props.projectId,
    enabled: () => !!handle.props.projectId,
  });
  const nameOf = (id: string): string => s.project.agentList.find((a) => a.id === id)?.name ?? id;

  return () => {
    const { agentId = '', role, changes, onChange } = handle.props;
    const all: Skill[] = q.data ?? [];
    const accepted = all
      .filter((k) => k.status === 'accepted')
      .sort((x, y) => Number(suggestsRole(y, role)) - Number(suggestsRole(x, role)) || x.name.localeCompare(y.name));
    const waiting = all.filter((k) => k.status === 'offered').length;
    const others = (k: Skill): Skill => ({ ...k, agents: (onTeam(k, s.project.agentList).agents ?? []).filter((a) => a !== agentId) });
    return (
      <fieldset mix={fieldCss} data-testid="agent-skills">
        <legend>
          <Icon icon={Cpu} size={14} />
          Skills
        </legend>
        <ErrorNote error={q.error} what="Skills" />
        {q.data && accepted.length === 0 ? (
          <p>
            No skill is accepted in this project yet. Skills come from the repository or a registry on the <a href={routes.skills.href()}>Skills</a> page.
          </p>
        ) : null}
        {accepted.map((k) => {
          const suggested = suggestsRole(k, role);
          const had = agentId !== '' && (k.agents ?? []).includes(agentId);
          const who = (others(k).agents ?? []).length > 0 ? `Also given to ${audienceText(others(k), nameOf)}` : 'Not given to another agent';
          return (
            <Checkbox
              key={k.name}
              label={
                <>
                  <code>{k.name}</code>
                  {k.description ? <span class="desc">{excerpt(k.description)}</span> : null}
                </>
              }
              description={`${suggested ? `Suggested for ${role}. ` : ''}${who}.`}
              checked={isGiven(changes, k.name, had)}
              onChange={(on) => onChange(toggleSkill(changes, k.name, had, on))}
            />
          );
        })}
        {waiting > 0 ? (
          <p>
            {waiting === 1 ? 'One skill waits' : `${waiting} skills wait`} for your decision on the <a href={routes.skills.href()}>Skills</a> page.
          </p>
        ) : null}
      </fieldset>
    );
  };
}
