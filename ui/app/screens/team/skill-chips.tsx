// "Skills and tools" of the agent editor panel (decision P137): what the agent has, as chips that can be taken away, and "+ Add"
// with the project's accepted skills and the library's MCP servers it does not have yet. Nothing changes until the panel is
// saved: the chips edit two change lists (skills through agent.update, servers through mcpserver.grant).

import { css, on, type Handle } from 'remix/component';
import { Plus, X } from 'lucide';
import type { McpLibraryServer, McpServer } from '../../api/mcpClient.ts';
import type { Skill } from '../../api/extTypes.ts';
import { query, services } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Menu, type MenuItem } from '../../ui/menu.tsx';
import { t } from '../../ui/tokens.ts';
import { isGiven, toggleSkill, type SkillChanges } from '../skills/skills.ts';

/** The servers granted and taken away in the panel (ids). */
export type ToolChanges = SkillChanges;

const rowCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: '6px',
  minWidth: '0',
  '& .chip': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '26px',
    padding: '0 4px 0 10px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: t.bgSurface,
    color: t.text,
    fontSize: t.textSm,
    whiteSpace: 'nowrap',
  },
  '& .chip[data-kind="tool"]': { background: t.bgRaised },
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
  '& .chip button:hover': { background: t.bgHover, color: t.text },
  '& .add': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '26px',
    minHeight: '0',
    padding: '0 10px',
    border: `1px dashed ${t.borderStrong}`,
    borderRadius: t.rFull,
    background: 'transparent',
    color: t.accentText,
    fontSize: t.textSm,
  },
  '& .none': { color: t.textFaint, fontSize: t.textSm },
});

export function SkillToolChips(
  handle: Handle<{
    projectId: string;
    agentId: string;
    skills: SkillChanges;
    onSkills: (c: SkillChanges) => void;
    tools: ToolChanges;
    onTools: (c: ToolChanges) => void;
  }>,
) {
  const s = services(handle);
  const skillsQ = query(handle, {
    load: () => s.client.skillList(handle.props.projectId),
    when: (e) => e.type.startsWith('skill.'),
    key: () => handle.props.projectId,
  });
  // a read that fails (an older core, no MCP servers) leaves the chips without tools
  const toolsQ = query(handle, {
    load: async (): Promise<{
      lib: McpLibraryServer[];
      project: McpServer[];
    }> => {
      const [lib, project] = await Promise.all([s.client.mcpLibList().catch(() => []), s.client.mcpServerList(handle.props.projectId).catch(() => [])]);
      return { lib, project };
    },
    when: (e) => e.type.startsWith('mcp'),
    key: () => handle.props.projectId,
  });
  return () => {
    const { agentId, skills, onSkills, tools, onTools } = handle.props;
    const accepted: Skill[] = (skillsQ.data ?? []).filter((k) => k.status === 'accepted');
    const hadSkill = (k: Skill) => (k.agents ?? []).includes(agentId);
    const lib = toolsQ.data?.lib ?? [];
    const granted = new Set((toolsQ.data?.project ?? []).filter((p) => p.agents.some((a) => a.agentId === agentId)).map((p) => p.id));
    const hadTool = (id: string) => granted.has(id);
    const mySkills = accepted.filter((k) => isGiven(skills, k.name, hadSkill(k)));
    const myTools = lib.filter((x) => isGiven(tools, x.id, hadTool(x.id)));
    const items: MenuItem[] = [
      ...accepted
        .filter((k) => !isGiven(skills, k.name, hadSkill(k)))
        .map((k) => ({
          name: `skill:${k.name}`,
          label: k.name,
        })),
      ...lib
        .filter((x) => !isGiven(tools, x.id, hadTool(x.id)))
        .map((x) => ({
          name: `tool:${x.id}`,
          label: `${x.display || x.id} (tool)`,
        })),
    ];
    return (
      <div mix={rowCss} data-testid="agent-skill-chips">
        {mySkills.map((k) => (
          <span key={`s:${k.name}`} class="chip" data-kind="skill" data-testid="skill-chip">
            {k.name}
            <button
              type="button"
              aria-label={`Take ${k.name} away`}
              title={`Take ${k.name} away`}
              mix={on('click', () => onSkills(toggleSkill(skills, k.name, hadSkill(k), false)))}
            >
              <Icon icon={X} size={12} />
            </button>
          </span>
        ))}
        {myTools.map((x) => (
          <span key={`t:${x.id}`} class="chip" data-kind="tool" data-testid="tool-chip">
            {x.display || x.id}
            <button
              type="button"
              aria-label={`Take ${x.display || x.id} away`}
              title={`Take ${x.display || x.id} away`}
              mix={on('click', () => onTools(toggleSkill(tools, x.id, hadTool(x.id), false)))}
            >
              <Icon icon={X} size={12} />
            </button>
          </span>
        ))}
        {mySkills.length + myTools.length === 0 ? <span class="none">None yet</span> : null}
        {items.length > 0 ? (
          <Menu
            label="Add a skill or tool"
            items={items}
            onSelect={(name) => {
              const [kind, id] = [name.slice(0, name.indexOf(':')), name.slice(name.indexOf(':') + 1)];
              if (kind === 'skill') {
                const k = accepted.find((x) => x.name === id);
                onSkills(toggleSkill(skills, id, k ? hadSkill(k) : false, true));
              } else onTools(toggleSkill(tools, id, hadTool(id), true));
            }}
            trigger={(mix) => (
              <button type="button" class="add" mix={mix} aria-label="Add a skill or tool">
                <Icon icon={Plus} size={12} />
                Add
              </button>
            )}
          />
        ) : null}
      </div>
    );
  };
}
