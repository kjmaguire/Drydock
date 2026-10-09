import { Command } from 'cmdk';
import { useEffect, useMemo, useState } from 'react';
import type { Agent, Room, Task } from '../api/types';
import { openDock, type DockTab } from '../dock/Dock';
import type { ThemeChoice } from '../lib/theme';
import { useUi } from '../state/ui';

interface Item {
  id: string;
  group: 'Navigate' | 'Tasks' | 'Agents' | 'Actions';
  label: string;
  hint?: string;
  keywords?: string[];
  run: () => void;
}

const SCREENS: [string, string][] = [
  ['#/needs-you', 'Needs you'],
  ['#/tasks', 'Tasks'],
  ['#/team', 'Team'],
  ['#/plan', 'Plan with PlanTeam'],
  ['#/pulls', 'Pull requests'],
  ['#/requirements', 'Requirements'],
  ['#/milestones', 'Milestones'],
  ['#/memory', 'Memory'],
  ['#/skills', 'Skills'],
  ['#/packs', 'Expert packs'],
  ['#/mcp', 'MCP servers of this project'],
  ['#/budgets', 'Budgets'],
  ['#/evals', 'Evals'],
  ['#/activity', 'Activity'],
  ['#/project', 'Project settings'],
  ['#/library', 'Library'],
  ['#/integrations', 'Integrations'],
  ['#/connections', 'Connections'],
  ['#/providers', 'Providers'],
  ['#/routing', 'Model routing'],
  ['#/backups', 'Backups'],
  ['#/data', 'Export and import'],
  ['#/support', 'Support bundle'],
  ['#/license', 'License'],
  ['#/settings', 'Settings and About'],
];

/**
 * The command palette (docs/ui.md 6.3), Ctrl+K. Navigation, tasks, agents and harmless actions only: nothing here approves,
 * merges, deletes or stops anything, so the palette can never bypass an approval.
 */
export function CommandPalette({
  rooms,
  tasks,
  agents,
  onTheme,
  onMessage,
}: {
  rooms: readonly Room[];
  tasks: readonly Task[];
  agents: readonly Agent[];
  onTheme: (t: ThemeChoice) => void;
  onMessage: (agentId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const ui = useUi();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    const onOpen = () => setOpen(true);
    window.addEventListener('keydown', onKey);
    window.addEventListener('drydock:palette', onOpen);
    return () => {
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('drydock:palette', onOpen);
    };
  }, []);

  const items = useMemo<Item[]>(() => {
    const go = (hash: string) => () => {
      window.location.hash = hash;
    };
    const dock = (t: DockTab) => () => openDock(t);
    const out: Item[] = [];
    for (const r of rooms.filter((r) => !r.archived)) {
      out.push({ id: `room:${r.id}`, group: 'Navigate', label: `${r.kind === 'dm' ? 'Direct message' : '#'}${r.kind === 'dm' ? '' : r.name}`, hint: r.kind === 'dm' ? r.name : r.kind, run: go(`#/room/${encodeURIComponent(r.id)}`) });
    }
    for (const [hash, label] of SCREENS) out.push({ id: `screen:${hash}`, group: 'Navigate', label, run: go(hash) });
    out.push({ id: 'task:new', group: 'Tasks', label: 'Create a task', run: go('#/tasks/new') });
    for (const t of tasks.slice(0, 200)) {
      out.push({ id: `task:${t.id}`, group: 'Tasks', label: `#${t.seq} ${t.title}`, hint: t.status, keywords: [String(t.seq), t.status], run: go(`#/task/${encodeURIComponent(t.id)}`) });
      if (t.runId) out.push({ id: `run:${t.id}`, group: 'Tasks', label: `Workflow of #${t.seq} ${t.title}`, keywords: ['graph', 'explain', 'run'], run: go(`#/task/${encodeURIComponent(t.id)}/run`) });
    }
    for (const a of agents) {
      out.push({ id: `inspect:${a.id}`, group: 'Agents', label: `Inspect ${a.name}`, hint: a.role, run: () => ui.openInspector(a.id) });
      out.push({ id: `dm:${a.id}`, group: 'Agents', label: `Message ${a.name}`, hint: a.role, run: () => onMessage(a.id) });
    }
    out.push(
      { id: 'act:terminal', group: 'Actions', label: 'Open a terminal', keywords: ['shell', 'console'], run: dock('terminal') },
      { id: 'act:diff', group: 'Actions', label: 'Show the diff of the current task', hint: 'Ctrl+Shift+D', run: dock('diff') },
      { id: 'act:logs', group: 'Actions', label: 'Show the logs of the current task', run: dock('logs') },
      { id: 'act:activity', group: 'Actions', label: 'Show model and token activity', run: dock('activity') },
      { id: 'act:dock', group: 'Actions', label: 'Toggle the dock', hint: 'Ctrl+J', run: () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', ctrlKey: true })) },
      { id: 'act:project', group: 'Actions', label: 'Add a project', run: go('#/project/new') },
      { id: 'act:theme-system', group: 'Actions', label: 'Theme: follow the system', run: () => onTheme('system') },
      { id: 'act:theme-dark', group: 'Actions', label: 'Theme: dark', run: () => onTheme('dark') },
      { id: 'act:theme-light', group: 'Actions', label: 'Theme: light', run: () => onTheme('light') },
    );
    return out;
  }, [rooms, tasks, agents, ui, onTheme, onMessage]);

  const groups: Item['group'][] = ['Navigate', 'Tasks', 'Agents', 'Actions'];
  return (
    <Command.Dialog open={open} onOpenChange={setOpen} label="Command palette" className="palette" overlayClassName="palette-overlay" contentClassName="palette-content" loop>
      <Command.Input placeholder="Go to, open or do... (Esc to close)" aria-label="Command" />
      <Command.List>
        <Command.Empty>Nothing matches.</Command.Empty>
        {groups.map((g) => (
          <Command.Group key={g} heading={g}>
            {items
              .filter((i) => i.group === g)
              .map((i) => (
                <Command.Item
                  key={i.id}
                  value={`${i.label} ${i.id}`}
                  keywords={i.keywords}
                  onSelect={() => {
                    setOpen(false);
                    i.run();
                  }}
                >
                  <span>{i.label}</span>
                  {i.hint ? <span className="muted palette-hint">{i.hint}</span> : null}
                </Command.Item>
              ))}
          </Command.Group>
        ))}
      </Command.List>
    </Command.Dialog>
  );
}
