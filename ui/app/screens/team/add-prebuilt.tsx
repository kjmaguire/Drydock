// Add a prebuilt agent to the current project (Team page): the gallery, whether to install the agent's skills and connect
// its MCP connectors, and what the core reports back (skills waiting for a decision, connectors that need sign-in).

import { css, type Handle } from 'remix/component';
import type { PrebuiltApplied } from '../../api/types.ts';
import { action, query, services } from '../../state/services.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { AgentGallery } from './agent-gallery.tsx';
import { skillLines } from './team.ts';

const wrapCss = css({ display: 'grid', gap: t.s4, '& ul': { margin: '4px 0 0', paddingLeft: '18px' } });
const optsCss = css({ display: 'grid', gap: t.s1 });

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

  const apply = async (id: string) => {
    busy = id;
    last = undefined;
    const r = await act.run('apply', () => s.client.agentCatalogApply({ id, projectId: handle.props.projectId, installSkills, connectMcp }));
    busy = '';
    if (r) {
      last = r;
      handle.props.onAdded(r);
      catalog.reload();
    }
    void handle.update();
  };

  return () => (
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
      <ErrorNote error={act.error ?? catalog.error} what="Agent not added" />
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
      <AgentGallery entries={catalog.data ?? []} onPick={(e) => void apply(e.id)} busy={busy} disabled={!handle.props.projectId} />
    </div>
  );
}
