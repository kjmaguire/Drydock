// An agent's own skills (decision P76: each agent is a specialist in its role, so its skills are its own, never the whole
// team's). AgentSkillList shows the skills given to the agent, each with Remove. AddSkills adds more: a project skill is given
// to the agent, and a Library skill is added to the project for this agent only (adding is the acceptance, P48). A skill that
// names the agent's role is suggested first.

import { css, on, type Handle } from 'remix/component';
import { BookOpen, Plus, X } from 'lucide';
import type { LibrarySkill, Skill } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { listOf, WindowedList } from '../../ui/windowed-list.tsx';
import { shortDescription, suggestsRole } from '../skills/skills.ts';

// The two long lists (ui/windowed-list.tsx, L37) are not a grid with a gap: each of their rows carries its own space below it.
export const rowSpace = css({ marginBottom: t.s2 });
/** One row of an agent's skills or tools: an icon, the name with a line under it, and the row's action at the end. */
export const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  padding: '10px 12px',
  borderRadius: '14px',
  background: t.bgSidebar,
  fontSize: t.textMd,
  minWidth: '0',
  '& .ico': { display: 'grid', placeItems: 'center', flex: 'none', width: '30px', height: '30px', borderRadius: '9px', background: t.bgSurface, border: `1px solid ${t.border}`, color: t.textMuted },
  '& .what': { flex: '1', minWidth: '0', display: 'grid', gap: '1px' },
  '& .what code': { fontSize: t.textMd, background: 'transparent', padding: '0' },
  '& .what small': { color: t.textFaint, fontSize: t.textSm, overflowWrap: 'anywhere' },
  '& .what small.desc': { color: t.textMuted },
  '& em': { marginLeft: 'auto', flex: 'none', fontStyle: 'normal', color: t.textFaint, fontSize: t.textSm },
});

const searchCss = css({ width: '100%', maxWidth: '420px' });
const tagsCss = css({ display: 'flex', flexWrap: 'wrap', gap: '6px', alignItems: 'center' });

/** The accepted project skills given to this agent, by name. */
export function agentSkills(all: readonly Skill[], agentId: string): Skill[] {
  return all.filter((k) => k.status === 'accepted' && (k.agents ?? []).includes(agentId)).sort((a, b) => a.name.localeCompare(b.name));
}

/** A skill the agent could be given: one the project has (accepted, not given to it yet) or one of the Library's. */
export interface SkillChoice {
  name: string;
  description: string;
  /** Where it comes from: the project, or a registry of the Library. */
  from: 'project' | 'library';
  registryId?: string;
  repo?: string;
  suggested: boolean;
}

/**
 * What can be added to an agent, matching the words typed: the project's accepted skills it does not have, then the Library's
 * available skills the project does not have. Suggested ones (naming the agent's role) come first, then by name.
 */
export function skillChoices(project: readonly Skill[], library: readonly LibrarySkill[], agent: Pick<Agent, 'id' | 'role'>, words: string): SkillChoice[] {
  const inProject = new Set(project.map((k) => k.name));
  const out: SkillChoice[] = [];
  for (const k of project) {
    if (k.status !== 'accepted' || (k.agents ?? []).includes(agent.id)) continue;
    out.push({ name: k.name, description: k.description, from: 'project', registryId: k.registryId, suggested: suggestsRole(k, agent.role) });
  }
  const seen = new Set<string>();
  for (const l of library) {
    if (l.status !== 'available' || l.gone || inProject.has(l.name) || seen.has(l.name)) continue;
    seen.add(l.name);
    out.push({ name: l.name, description: l.description, from: 'library', registryId: l.registryId, repo: l.repo, suggested: suggestsRole(l, agent.role) });
  }
  const terms = words.toLowerCase().split(/\s+/).filter(Boolean);
  const hits = terms.length === 0 ? out : out.filter((c) => terms.every((w) => `${c.name} ${c.description} ${c.repo ?? ''}`.toLowerCase().includes(w)));
  return hits.sort((a, b) => Number(b.suggested) - Number(a.suggested) || Number(a.from === 'library') - Number(b.from === 'library') || a.name.localeCompare(b.name));
}

/** The skills given to an agent, each with Remove; the first `limit` of them when a limit is set. */
export function AgentSkillList(handle: Handle<{ agent: Agent; skills: readonly Skill[]; full?: boolean; limit?: number; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const remove = (k: Skill) =>
    act.run(k.name, async () => {
      await s.client.agentUpdate({ id: handle.props.agent.id, removeSkills: [k.name] });
      handle.props.onChanged();
    });
  return () => {
    const { agent, skills, full, limit } = handle.props;
    const mine = agentSkills(skills, agent.id);
    const shown = limit ? mine.slice(0, limit) : mine;
    return (
      <>
        <ErrorNote error={act.error} what="Skills" />
        <WindowedList
          {...listOf(
            shown,
            (k) => k.name,
            (k) => (
              <div role="listitem" mix={[rowCss, rowSpace]} data-testid="agent-skill" data-skill={k.name}>
                <span class="ico" aria-hidden="true">
                  <Icon icon={BookOpen} size={15} />
                </span>
                <span class="what">
                  <code>{k.name}</code>
                  {k.description ? <small class="desc">{full ? k.description : shortDescription(k.description)}</small> : null}
                </span>
                <IconButton
                  icon={X}
                  label={`Remove ${k.name} from ${agent.name}`}
                  disabled={act.busy !== ''}
                  data-testid="agent-skill-remove"
                  mix={on('click', () => void remove(k))}
                />
              </div>
            ),
          )}
          label={`${agent.name}'s skills`}
          role="list"
          testId="agent-skills-list"
          maxHeight={640}
          estimate={64}
        />
      </>
    );
  };
}

/** Add skills to one agent: from the project's skills, or straight from the Library for this agent only. */
export function AddSkills(handle: Handle<{ agent: Agent; projectId: string; skills: readonly Skill[]; onChanged: () => void }>) {
  const s = services(handle);
  const lib = query(handle, { load: () => s.client.skillLibraryList(), when: (e) => e.type === 'skill.library_synced' });
  const act = action(handle);
  let words = '';
  let added = '';
  const LIMIT = 30;
  const add = (c: SkillChoice) =>
    act.run(c.name, async () => {
      const { agent, projectId, onChanged } = handle.props;
      if (c.from === 'project') await s.client.agentUpdate({ id: agent.id, addSkills: [c.name] });
      else await s.client.skillLibraryAdd(projectId, c.registryId!, c.name, 'agents', [agent.id]);
      added = c.name;
      onChanged();
    });
  return () => {
    const { agent, skills } = handle.props;
    const choices = skillChoices(skills, lib.data ?? [], agent, words);
    const shown = choices.slice(0, LIMIT);
    return (
      <div style={{ display: 'grid', gap: t.s3 }} data-testid="agent-add-skills">
        <input
          type="search"
          mix={[searchCss, on('input', (e) => ((words = e.currentTarget.value), void handle.update()))]}
          value={words}
          placeholder="Search the project's skills and the Library"
          aria-label={`Search skills to add to ${agent.name}`}
        />
        <ErrorNote error={lib.error ?? act.error} what="Skills" />
        {added ? (
          <p class="quiet" role="status" data-testid="agent-skill-added">
            {added} was given to {agent.name}.
          </p>
        ) : null}
        {lib.loading && !lib.data ? <Loading /> : null}
        {lib.data && choices.length === 0 ? (
          <p class="quiet" data-testid="agent-skill-none">
            {words ? (
              'No skill matches these words.'
            ) : (lib.data ?? []).length === 0 && skills.every((k) => k.status !== 'accepted' || (k.agents ?? []).includes(agent.id)) ? (
              <>
                Nothing to add yet: the <a href={routes.librarySkills.href()}>Library</a> fills from your registries at start and twice a day.
              </>
            ) : (
              'Every skill of the project and the Library is given to this agent already.'
            )}
          </p>
        ) : null}
        <WindowedList
          {...listOf(
            shown,
            (c) => `${c.from}:${c.name}`,
            (c) => (
              <div role="listitem" mix={[rowCss, rowSpace]} data-testid="agent-skill-choice" data-skill={c.name} data-from={c.from}>
                <span class="ico" aria-hidden="true">
                  <Icon icon={BookOpen} size={15} />
                </span>
                <span class="what">
                  <span mix={tagsCss}>
                    <code>{c.name}</code>
                    {c.suggested ? <Badge tone="ok">suggested for {agent.role}</Badge> : null}
                    <Badge>{c.from === 'project' ? 'in this project' : c.repo}</Badge>
                  </span>
                  {c.description ? <small class="desc">{shortDescription(c.description)}</small> : null}
                </span>
                <Button size="sm" icon={Plus} disabled={act.busy !== ''} data-testid="agent-skill-add" mix={on('click', () => void add(c))}>
                  {act.busy === c.name ? 'Adding...' : 'Add'}
                </Button>
              </div>
            ),
          )}
          label={`Skills to add to ${agent.name}`}
          role="list"
          testId="agent-skill-choices"
          maxHeight={640}
          estimate={72}
        />
        {choices.length > LIMIT ? (
          <p class="quiet">
            Showing {LIMIT} of {choices.length}. Type to narrow them.
          </p>
        ) : null}
      </div>
    );
  };
}
