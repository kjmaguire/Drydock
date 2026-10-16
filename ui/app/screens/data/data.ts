// Pure helpers for the Export and import screen.

import type { ImportStats } from '../../api/pkgTypes.ts';
import type { Project } from '../../api/types.ts';

/** The project to export: the person's pick if it still exists, else the selected project, else the first one. */
export function exportProjectId(projects: Project[], picked: string, current: Project | undefined): string {
  if (projects.some((p) => p.id === picked)) return picked;
  return current?.id ?? projects[0]?.id ?? '';
}

/** A preview belongs to the choices it was made for. */
export function exportKey(projectId: string, includeRuns: boolean): string {
  return `${projectId}|${includeRuns}`;
}

/** Everything an import does not trust, as [label, count, what it means]. Providers without keys are listed separately. */
export function pendingRows(p: ImportStats): [string, number, string][] {
  return [
    ['Memory entries proposed', p.memoryPending, 'agents do not see them until you accept each one'],
    ['Skills offered', p.skillsPending, 'none is accepted or run until you accept it'],
    ['Requirement chunks quarantined', p.requirementChunksQuarantined, 'held until you release them'],
    ['Routing rules disabled', p.routingRulesDisabled, 'turn them on yourself if you want them'],
    ['Runs not resumed (cancelled)', p.runsNotResumed, 'work that was in progress is not restarted'],
    ['Tasks blocked', p.tasksBlocked, 'blocked until you decide'],
    ['Approvals cancelled', p.approvalsCancelled, 'open approvals from the source are not carried over'],
    ['Agent purposes cleared', p.agentPurposesCleared ?? 0, 'instructions written in the archive are not sent to your agents; write them again if you want them'],
    ['Agent permission modes lowered', p.agentPermissionModesLowered ?? 0, 'a mode that skips approvals is set back to acceptEdits'],
  ];
}
