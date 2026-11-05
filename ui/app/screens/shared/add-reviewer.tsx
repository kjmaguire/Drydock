// A task needs a worker and a different agent as its reviewer (P142: a task cannot start with one agent). A project made with
// one builder (New project's default) is offered the missing Code Reviewer from the catalog in one click (agentcatalog.apply),
// on the Crew page and in Work's crew rail; a project with no agent is offered the suggested team (agentcatalog.applyMany: a
// Team Lead, a Backend Engineer and a Code Reviewer). Catalog agents keep asking for approvals as any agent does.

import { css, on, type Handle } from 'remix/component';
import { UserPlus, Users } from 'lucide';
import type { Agent } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { SUGGESTED } from '../project/wizard.ts';
import { pickPair } from '../workspace/start.ts';

/** The catalog's reviewer (internal/agentcatalog/catalog.yaml). */
export const CODE_REVIEWER = 'code-reviewer';
/** The CLIs the catalog's Code Reviewer can run on: its preferred one first. */
const REVIEWER_CLIS = ['claude', 'gemini', 'codex'];

/** The agent that is alone in a crew that cannot run a task (no different reviewer), or undefined. */
export function aloneInCrew(agents: readonly Agent[]): Agent | undefined {
  const usable = agents.filter((a) => !/lead/i.test(a.role));
  if (usable.length === 0 || pickPair(agents)) return undefined;
  return usable[0];
}

/** Adds the catalog's Code Reviewer to the project, on the builder's CLI when the reviewer can run there. */
export function addCodeReviewer(s: ReturnType<typeof services>, projectId: string, builder: Agent | undefined): Promise<Agent> {
  const adapter = builder && REVIEWER_CLIS.includes(builder.adapter) ? builder.adapter : undefined;
  return s.client.agentCatalogApply({ id: CODE_REVIEWER, projectId, adapter, installSkills: true, connectMcp: true }).then((r) => r.agent);
}

const noteCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  fontSize: t.textSm,
  minWidth: '0',
  '& p': { margin: '0', overflowWrap: 'anywhere' },
  '& button': { justifySelf: 'start' },
});

/** "Tasks need a reviewer besides <builder>" with Add Code Reviewer; renders nothing when the crew can run a task. */
export function AddReviewerNote(handle: Handle<{ projectId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const act = action(handle);
  return () => {
    const { projectId, agents } = handle.props;
    const alone = aloneInCrew(agents);
    if (!alone || !projectId) return null;
    return (
      <div mix={noteCss} role="group" aria-label="Add a reviewer" data-testid="add-reviewer">
        <p>Tasks need a reviewer besides {alone.name}.</p>
        <ErrorNote error={act.error} what="Not added" />
        <Button size="sm" icon={UserPlus} disabled={!!act.busy} mix={on('click', () => void act.run('add', () => addCodeReviewer(s, projectId, alone)))}>
          {act.busy ? 'Adding...' : 'Add Code Reviewer'}
        </Button>
      </div>
    );
  };
}

/** For a project with no agent: the suggested team in one click. */
export function AddSuggestedTeam(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const act = action(handle);
  let failed: string[] = [];
  const add = async () => {
    const r = await act.run('team', () => s.client.agentCatalogApplyMany({ projectId: handle.props.projectId, items: SUGGESTED.map((id) => ({ id })), installSkills: true, connectMcp: true }));
    failed = (r ?? []).filter((x) => x.error).map((x) => `${x.id}: ${x.error}`);
    void handle.update();
  };
  return () => (
    <div style={{ display: 'grid', gap: t.s2, justifyItems: 'start' }}>
      <ErrorNote error={act.error} what="Not added" />
      {failed.length ? (
        <p role="alert" data-testid="add-team-failed" style={{ margin: '0', fontSize: t.textSm }}>
          Not added: {failed.join('; ')}
        </p>
      ) : null}
      <Button variant="primary" icon={Users} disabled={!!act.busy || !handle.props.projectId} data-testid="add-suggested-team" mix={on('click', () => void add())}>
        {act.busy ? 'Adding...' : 'Add the suggested team'}
      </Button>
      <span style={{ fontSize: t.textSm, color: t.textMuted }}>A Team Lead, a Backend Engineer and a Code Reviewer from the catalog.</span>
    </div>
  );
}
