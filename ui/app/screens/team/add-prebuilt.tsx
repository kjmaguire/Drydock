// Add a prebuilt agent to the current project (Team page): the gallery, whether to install the agent's skills and connect
// its MCP connectors, and what the core reports back (skills waiting for a decision, connectors that need sign-in).
// An entry whose permission mode lets the agent act without asking is not added by one click: it asks for the person's
// confirmation first (screens/shared/skip-approvals.tsx), and the core is told (audit 2026-10-16, M1).

import { css, on, type Handle } from 'remix/component';
import type { PrebuiltAgent, PrebuiltApplied } from '../../api/types.ts';
import { confirmationFor, needsConfirmation, skipsApprovals, splitConfirmation } from '../../lib/permission.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { SkipApprovals } from '../shared/skip-approvals.tsx';
import { AgentGallery } from './agent-gallery.tsx';
import { skillLines } from './team.ts';

const wrapCss = css({ display: 'grid', gap: t.s4, '& ul': { margin: '4px 0 0', paddingLeft: '18px' } });
const optsCss = css({ display: 'grid', gap: t.s1 });
const confirmCss = css({ display: 'grid', gap: t.s3, padding: t.s3, border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, '& p': { margin: '0' }, '& .row': { display: 'flex', gap: t.s2, flexWrap: 'wrap' } });

/** What an apply did, in sentences for the person. */
export function appliedLines(r: PrebuiltApplied): string[] {
  const out = skillLines(r.skills);
  if (r.mcp.granted.length) out.push(`Connectors: ${r.mcp.granted.join(', ')}.`);
  for (const x of r.mcp.skipped) out.push(`Connector ${x.id ?? x.name} not connected: ${x.reason}`);
  return [...out, ...r.notes];
}

export function AddPrebuilt(handle: Handle<{ projectId: string; onAdded: (r: PrebuiltApplied) => void }>) {
  const s = services(handle);
  const act = action(handle);
  const catalog = query(handle, { load: () => s.client.agentCatalogList(handle.props.projectId), key: () => handle.props.projectId });
  let installSkills = true;
  let connectMcp = true;
  let busy = '';
  let last: PrebuiltApplied | undefined;
  // an entry whose permission mode skips approvals, waiting for the person's confirmation, and the mode they ticked the box for
  let pending: PrebuiltAgent | undefined;
  let confirmedMode = '';

  /** The mode the core gives the agent: the entry's own (no mode is sent from here). */
  const modeOf = (e: PrebuiltAgent) => e.preferred.permissionMode ?? '';

  const apply = async (e: PrebuiltAgent) => {
    const mode = modeOf(e);
    if (needsConfirmation(mode, confirmedMode)) return;
    busy = e.id;
    last = undefined;
    const r = await act.run('apply', () =>
      s.client.agentCatalogApply({ id: e.id, projectId: handle.props.projectId, installSkills, connectMcp, ...confirmationFor(mode, confirmedMode) }),
    );
    busy = '';
    if (r) {
      last = r;
      pending = undefined;
      confirmedMode = '';
      handle.props.onAdded(r);
      catalog.reload();
    }
    void handle.update();
  };

  /** One click adds an entry that keeps approvals; one that skips them first asks. */
  const pick = (e: PrebuiltAgent) => {
    if (skipsApprovals(modeOf(e))) {
      pending = e;
      confirmedMode = '';
      last = undefined;
      act.clear();
      void handle.update();
      return;
    }
    pending = undefined;
    void apply(e);
  };

  return () => {
    const { confirm: refusal, other: failure } = splitConfirmation(act.error);
    const mode = pending ? modeOf(pending) : '';
    return (
      <div mix={wrapCss} data-testid="add-prebuilt">
        <div mix={optsCss}>
          <Checkbox
            label="Install the skills this agent comes with"
            description="Pinned skills from public registries, checked by Drydock before use."
            checked={installSkills}
            onChange={(v) => ((installSkills = v), void handle.update())}
          />
          <Checkbox
            label="Connect its MCP connectors"
            description="Adds them from the built-in catalog when needed; some need sign-in afterwards."
            checked={connectMcp}
            onChange={(v) => ((connectMcp = v), void handle.update())}
          />
        </div>
        <ErrorNote error={failure ?? catalog.error} what="Agent not added" />
        {pending || refusal ? (
          <div mix={confirmCss} data-testid="prebuilt-confirm">
            {pending ? (
              <p>
                <strong>{pending.name}</strong> comes with the permission mode "{mode}".
              </p>
            ) : null}
            <SkipApprovals mode={mode} confirmed={confirmedMode} onConfirm={(m) => ((confirmedMode = m), void handle.update())} who={pending?.name ?? 'this agent'} error={refusal} />
            {pending ? (
              <div class="row">
                <Button variant="primary" disabled={!!busy || needsConfirmation(mode, confirmedMode)} mix={on('click', () => void apply(pending!))}>
                  {busy === pending.id ? 'Adding...' : `Add ${pending.name}`}
                </Button>
                <Button
                  disabled={!!busy}
                  mix={on('click', () => {
                    pending = undefined;
                    confirmedMode = '';
                    act.clear();
                  })}
                >
                  Cancel
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
        {last ? (
          <Notice tone="ok" role="status" title={`${last.agent.name} joined the team.`} testId="prebuilt-added">
            {appliedLines(last).length ? (
              <ul>
                {appliedLines(last).map((l, i) => (
                  <li key={i}>{l}</li>
                ))}
              </ul>
            ) : null}
          </Notice>
        ) : null}
        {catalog.loading && !catalog.data ? <Loading label="Loading prebuilt agents..." /> : null}
        <AgentGallery entries={catalog.data ?? []} onPick={pick} busy={busy} disabled={!handle.props.projectId} />
      </div>
    );
  };
}
