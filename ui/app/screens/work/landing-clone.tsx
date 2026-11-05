// The clone a project made before its clone landed waits for (B12): New project's Clone makes the project at once and the core
// clones in the background (project.setup with repo.later). While git runs, Work shows "Cloning gocalc… receiving 42%" with
// Cancel (CloneProgress); when the clone lands it is the project's repository and the line goes; when it fails or is cancelled
// the line says why, with Project settings, where Clone and attach tries again. Asked only for a project with no repository.

import { css, type Handle } from 'remix/component';
import { TriangleAlert } from 'lucide';
import type { LandingClone } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { CloneProgress } from '../project/clone-progress.tsx';

const lineCss = css({
  display: 'flex',
  alignItems: 'flex-start',
  gap: t.s2,
  margin: `0 ${t.s3} ${t.s2}`,
  padding: `${t.s1} ${t.s2}`,
  borderRadius: t.rMd,
  fontSize: t.textSm,
  '&[data-state="failed"]': { background: t.warningSoft },
  '& > svg': { flex: 'none', marginTop: '2px', color: t.warning },
  '& .text': { flex: '1', minWidth: '0', overflowWrap: 'anywhere' },
});

/** What the line says for a clone that was not attached. */
export function landingFailedText(l: Pick<LandingClone, 'repo' | 'error'>): string {
  const why = (l.error ?? '').trim();
  return `The project has no repository yet: ${why || `the clone of ${l.repo} did not land`}.`.replace(/\.\.$/, '.');
}

export function LandingCloneLine(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const state = query(handle, {
    load: async (): Promise<LandingClone | null> => {
      try {
        return await s.client.projectCloneState(handle.props.projectId);
      } catch {
        return null; // an older core has no landing clones
      }
    },
    when: (e) => (e.type === 'project.clone_landed' || e.type === 'project.clone_failed') && e.projectId === handle.props.projectId,
  });
  return () => {
    const l = state.data;
    if (!l) return null;
    if (l.running) {
      return (
        <div mix={lineCss} data-testid="landing-clone" data-state="running">
          <CloneProgress repo={l.repo} />
        </div>
      );
    }
    return (
      <div mix={lineCss} role="status" data-testid="landing-clone" data-state="failed">
        <Icon icon={TriangleAlert} size={13} />
        <span class="text">{landingFailedText(l)}</span>
        <a href={routes.project.href()} data-testid="landing-clone-settings">
          Clone and attach
        </a>
      </div>
    );
  };
}
