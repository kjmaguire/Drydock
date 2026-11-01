// Pure helpers of "Describe a new agent" (decision P138): the starter sentences, the agent.create spec a draft becomes, and the
// line that says who drafted it.

import type { AgentDraft } from '../../api/libraryClient.ts';
import type { AgentSpec } from '../../api/types.ts';

/** The starter chips under the description box: a label and the sentence it puts in the box. */
export const STARTERS: readonly { label: string; text: string }[] = [
  {
    label: 'Builder',
    text: 'Builds features end to end: reads the code first, keeps each change small and tested.',
  },
  {
    label: 'Reviewer',
    text: 'Reviews every change before it is merged. Never edits code, just comments.',
  },
  {
    label: 'Tester',
    text: 'Writes and runs the tests, and reproduces each bug with a failing test first.',
  },
  {
    label: 'Docs writer',
    text: 'Keeps the README and the docs true to the code.',
  },
  {
    label: 'Team lead',
    text: 'A team lead that splits the work and keeps the crew on task.',
  },
  {
    label: 'Planner',
    text: 'Plans each feature and writes the spec before anyone codes.',
  },
];

/** The agent.create spec of a (possibly edited) draft. Empty values are left out: the core then uses its defaults. */
export function specFromDraft(d: AgentDraft, projectId: string): AgentSpec {
  const spec: AgentSpec = {
    projectId,
    name: d.name.trim(),
    role: d.role.trim(),
    adapter: d.adapter,
  };
  if (d.model.trim()) spec.model = d.model.trim();
  if (d.effort.trim()) spec.effort = d.effort.trim();
  if (d.permissionMode) spec.permissionMode = d.permissionMode;
  if (d.purpose.trim()) spec.purpose = d.purpose.trim();
  if (d.skills.length) spec.skills = [...d.skills];
  return spec;
}

/** The quiet line under the draft. */
export function draftSourceNote(d: AgentDraft | undefined): string {
  if (!d) return 'One sentence is enough. Nothing is saved until you press Create agent.';
  return d.source === 'model'
    ? 'Drafted by your default model. Nothing is saved until you press Create agent.'
    : 'Drafted from your words on this PC (no model was used). Nothing is saved until you press Create agent.';
}

/** A one-line summary of the draft under its name: the role and the first sentence of its instructions. */
export function draftSummary(d: Pick<AgentDraft, 'role' | 'purpose'>): string {
  const first = d.purpose.split(/(?<=[.!?])\s/)[0]?.trim() ?? '';
  const s = first.length > 140 ? `${first.slice(0, 140)}...` : first;
  return s || d.role;
}
