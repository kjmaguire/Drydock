// Security scan of the project (its Security pill): Scan again (R) reads the project's working tree with three engines at once
// (secrets, dependencies, code patterns), with the engines' tiles, the findings and the selected finding's pane, and the recent
// scans of the project. The screen itself is ScanPanel (scan-panel.tsx), shared with the workspace Security scan that scans any
// folder (decision P61); its target field names the project's repository.

import { type Handle } from 'remix/component';
import { ShieldCheck } from 'lucide';
import { projectHasRepo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Notice } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { ScanPanel, TargetBox } from './scan-panel.tsx';
import { projectScope } from './scan-scope.ts';
import { rulesWords } from './security.ts';

/** Whether the scan also runs by a rule the person set (H5), with the way to Project settings where the rules are set. */
export function ScanRulesNote(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.securityScheduleGet(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type === 'security.schedule' && e.projectId === handle.props.projectId,
  });
  return () => {
    if (!q.data) return null;
    const on = rulesWords(q.data);
    return (
      <p data-testid="scan-rules-note" style={{ margin: '0' }}>
        {on ? (
          <>
            Also runs {on}. <a href={routes.project.href()}>Change the rules</a>
          </>
        ) : (
          <>
            Runs when you press Scan. <a href={routes.project.href()}>Run it after a merge, a sync or weekly</a>
          </>
        )}
      </p>
    );
  };
}

export function SecurityScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);

  return () => {
    const project = s.workspace.project;
    const pid = s.workspace.projectId;
    const hasRepo = projectHasRepo(project);
    if (!pid) {
      return (
        <Page title="Security" icon={ShieldCheck} testId="security-scan">
          <Notice tone="info">Choose a project to scan.</Notice>
        </Page>
      );
    }
    return (
      <ScanPanel
        scope={projectScope(s.client, pid)}
        canScan={!!project && hasRepo}
        // named as its pill under Overview names it; the workspace's page that scans any folder is the Security scan
        title="Security"
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
          ) : project ? (
            <ScanRulesNote key={pid} projectId={pid} />
          ) : undefined
        }
      />
    );
  };
}
