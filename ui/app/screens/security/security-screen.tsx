// Security scan of the project: one button scans the project's working tree with three engines at once (secrets,
// dependencies, code patterns), with live progress, the findings, the tools on this computer and the history of scans. The
// scan itself is ScanPanel (scan-panel.tsx), shared with the workspace Security page that scans any folder (decision P61).

import { type Handle } from 'remix/component';
import { ShieldCheck } from 'lucide';
import { projectHasRepo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { Notice } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { ScanPanel } from './scan-panel.tsx';
import { projectScope } from './scan-scope.ts';

export function SecurityScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);

  return () => {
    const project = s.workspace.project;
    const pid = s.workspace.projectId;
    const hasRepo = projectHasRepo(project);
    return (
      <Page title="Security scan" icon={ShieldCheck} testId="security-scan" subtitle={project?.name} guide="scan">
        {!project ? <Notice tone="info">Choose a project to scan.</Notice> : null}
        {project && !hasRepo ? (
          <Notice tone="warn" testId="security-no-repo">
            The scan needs a repository. <a href={routes.project.href()}>Open Project settings</a> to attach one.
          </Notice>
        ) : null}
        {pid ? <ScanPanel scope={projectScope(s.client, pid)} canScan={!!project && hasRepo} /> : null}
      </Page>
    );
  };
}
