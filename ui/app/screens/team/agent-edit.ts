// Pure helpers of the agent editor panel and the Crew card's chips (decision P137): the permission modes in words, the choices
// the panel offers while the CLI's capabilities load or when they cannot be read, the chip text, and the update a Save sends.

import type { Agent, AgentUpdate, Capabilities } from '../../api/types.ts';
import { skipsApprovals } from '../../lib/permission.ts';
import type { Segment } from '../../ui/segmented.tsx';

/**
 * The three permission modes a person chooses between, in words (the mockup's Read only / Asks before edits / Edits freely),
 * mapped to the modes the CLIs take: plan reads and plans without editing, default asks before each edit, acceptEdits edits
 * its worktree freely (what a new agent gets).
 */
export const PERMISSION_SEGMENTS: readonly Segment[] = [
  {
    value: 'plan',
    label: 'Read only',
    hint: 'Reads and comments; never edits files (plan)',
  },
  {
    value: 'default',
    label: 'Asks before edits',
    hint: 'Asks you before each edit (default)',
  },
  {
    value: 'acceptEdits',
    label: 'Edits freely',
    hint: 'Edits its own worktree without asking (acceptEdits)',
  },
];

/**
 * What the core uses when it is told nothing, per CLI, so the panel is never stuck while the CLI's capabilities load or when
 * they cannot be read (decision P137: the owner could not change model or effort at all). The values are the claude adapter's
 * own lists (internal/agents/claude/adapter.go); another CLI gets free text.
 */
export const KNOWN_CAPS: Record<string, Pick<Capabilities, 'models' | 'efforts' | 'permissionModes'>> = {
  claude: {
    models: ['fable', 'opus', 'sonnet', 'haiku'],
    efforts: ['low', 'medium', 'high', 'xhigh', 'max'],
    permissionModes: ['default', 'acceptEdits', 'plan', 'dontAsk', 'auto', 'bypassPermissions'],
  },
};

/** The models offered for a CLI: its own list, else the known one, else none (the field is then free text). */
export function modelList(adapter: string, caps: Capabilities | undefined): string[] {
  return caps?.models?.length ? [...caps.models] : [...(KNOWN_CAPS[adapter]?.models ?? [])];
}

/** The effort levels offered for a CLI: its own list, else the known one, else none (free text). */
export function effortList(adapter: string, caps: Capabilities | undefined): string[] {
  return caps?.efforts?.length ? [...caps.efforts] : [...(KNOWN_CAPS[adapter]?.efforts ?? [])];
}

/** The permission modes a CLI takes: its own list, else the known one, else none (the CLI's default only). */
export function modeList(adapter: string, caps: Capabilities | undefined): string[] {
  return caps?.permissionModes?.length ? [...caps.permissionModes] : [...(KNOWN_CAPS[adapter]?.permissionModes ?? [])];
}

/** The effort segments: the CLI's default, then each level; a saved value outside the list stays visible. */
export function effortSegments(efforts: readonly string[], current: string): Segment[] {
  return [
    { value: '', label: 'default', hint: "The CLI's own default" },
    ...efforts.map((e) => ({ value: e, label: e })),
    ...(current && !efforts.includes(current)
      ? [
          {
            value: current,
            label: current,
            hint: 'not in the list this CLI reports',
          },
        ]
      : []),
  ];
}

/**
 * The permission choices for a CLI: the three segments it takes, and the other modes it lists (offered under "Other"). A
 * current mode outside both is kept among the others, so it is never silently replaced.
 */
export function permissionChoices(modes: readonly string[], current: string): { segments: Segment[]; others: string[] } {
  const known = new Set(PERMISSION_SEGMENTS.map((s) => s.value));
  const segments = PERMISSION_SEGMENTS.filter((s) => modes.includes(s.value));
  const others = modes.filter((m) => !known.has(m));
  if (current && !modes.includes(current) && !others.includes(current)) others.push(current);
  return { segments, others };
}

/** A permission mode in words: the chip on a Crew card and the panel. */
export function permissionWords(mode: string | undefined): string {
  const seg = PERMISSION_SEGMENTS.find((s) => s.value === mode);
  if (seg) return seg.label;
  if (!mode) return 'CLI default';
  if (skipsApprovals(mode)) return 'Skips approvals';
  return mode;
}

/** The "model · effort" chip of an agent ("sonnet · medium", "default model"). */
export function modelChip(a: Pick<Agent, 'model' | 'effort'>): string {
  const m = a.model || 'default model';
  return a.effort ? `${m} · ${a.effort}` : m;
}

/** What the panel edits. */
export interface AgentEdits {
  name: string;
  role: string;
  model: string;
  effort: string;
  permissionMode: string;
  purpose: string;
}

export const editsOf = (a: Agent): AgentEdits => ({
  name: a.name,
  role: a.role,
  model: a.model ?? '',
  effort: a.effort ?? '',
  permissionMode: a.permissionMode ?? '',
  purpose: a.purpose ?? '',
});

/**
 * The agent.update a Save sends: only the fields that changed (an "Everywhere" save carries exactly these to the template and
 * the other agents made from it, nothing the person did not touch).
 */
export function updateFor(a: Agent, e: AgentEdits): AgentUpdate {
  const u: AgentUpdate = { id: a.id };
  const was = editsOf(a);
  for (const k of ['name', 'role', 'model', 'effort', 'permissionMode', 'purpose'] as const) {
    const v = e[k].trim();
    if (v !== was[k].trim()) u[k] = v;
  }
  return u;
}

/** The line under the This project | Everywhere switch. */
export function scopeNote(a: Pick<Agent, 'name' | 'templateId'>, everywhere: boolean, projectName: string, chip: string): string {
  if (!everywhere) return `${chip} is used for ${a.name} in ${projectName || 'this project'} only. Other projects keep their own settings.`;
  if (a.templateId) return `Also saves to ${a.name}'s template and to every agent made from it in other projects.`;
  return `Also saves ${a.name} as a template in your library; agents you add from it get these settings.`;
}

/** What an "Everywhere" save did, in one line. */
export function everywhereNote(ev: { templateName: string; created: boolean; agents: number; skipped: string[] }): string {
  const head = ev.created
    ? `Saved as the template ${ev.templateName}.`
    : `Saved to the template ${ev.templateName}${ev.agents ? ` and ${ev.agents} more ${ev.agents === 1 ? 'agent' : 'agents'}` : ''}.`;
  return ev.skipped.length ? `${head} Not changed: ${ev.skipped.join('; ')}` : head;
}

/** The permission choices of the New agent form's list: "Edits freely" (what the core gives when told nothing) first. */
export function permissionOptions(modes: readonly string[]): { value: string; label: string; hint?: string }[] {
  return [
    { value: '', label: 'Edits freely (acceptEdits, the default)' },
    ...modes
      .filter((m) => m !== 'acceptEdits')
      .map((m) => ({ value: m, label: permissionWords(m) === m ? m : `${permissionWords(m)} (${m})`, hint: skipsApprovals(m) ? 'skips approvals' : undefined })),
  ];
}
