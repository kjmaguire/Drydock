// Security scan of the project (its Security pill): Scan again (R) reads the project's working tree with three engines at once
// (secrets, dependencies, code patterns), with the engines' tiles, the findings and the selected finding's pane, and the recent
// scans of the project. The screen itself is ScanPanel (scan-panel.tsx), shared with the workspace Security scan that scans any
// folder (decision P61); its target field names the project's repository.

import { type Handle } from 'remix/component';
import { ShieldCheck } from 'lucide';
import { projectHasRepo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { Notice } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { ScanPanel, TargetBox } from './scan-panel.tsx';
import { projectScope } from './scan-scope.ts';

export function SecurityScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);

  return () => {
    const project = s.workspace.project;
    const pid = s.workspace.projectId;
    const hasRepo = projectHasRepo(project);
    if (!pid) {
      return (
        <Page title="Security scan" icon={ShieldCheck} testId="security-scan">
          <Notice tone="info">Choose a project to scan.</Notice>
        </Page>
      );
    }
    return (
      <ScanPanel
        scope={projectScope(s.client, pid)}
        canScan={!!project && hasRepo}
        title="Security scan"
        testId="security-scan"
        nameOf={() => project?.name ?? 'project'}
        target={
          <TargetBox note={project?.name}>
            <span class="value" title={project?.repoPath || undefined} data-testid="scan-target-path">
              {project?.repoPath || 'No repository yet'}
            </span>
          </TargetBox>
        }
        notice={
          project && !hasRepo ? (
            <Notice tone="warn" testId="security-no-repo">
              The scan needs a repository. <a href={routes.project.href()}>Open Project settings</a> to attach one.
            </Notice>
          ) : undefined
        }
      />
    );
  };
}
