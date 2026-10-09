// The application shell. The SPA router renders every route's content inside it (render transform in router.tsx), and
// the same Shell instance stays mounted across navigations, so the sidebar (with the project switcher), the dock (and a running terminal)
// and the details panel keep their state while the center changes.

import { css, navigate, type Handle, type RemixNode } from 'remix/component';
import { isWorkspaceRoute, parseRoute } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { ProjectCreateScreen } from '../screens/project/project-create-screen.tsx';
import { services, watch } from '../state/services.tsx';
import { ErrorNote, Loading } from '../ui/feedback.tsx';
import { Page } from '../ui/layout.tsx';
import { t } from '../ui/tokens.ts';
import { Details } from './details.tsx';
import { Dock } from './dock.tsx';
import { LicenseBanner } from './license-banner.tsx';
import { CommandPalette } from './palette.tsx';
import { Sidebar } from './sidebar.tsx';

const rootCss = css({ display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' });
const appCss = css({ display: 'flex', flex: '1', minHeight: '0' });
const centerCss = css({ display: 'flex', flexDirection: 'column', flex: '1', minWidth: '0', minHeight: '0', background: t.bgApp });
const mainCss = css({ flex: '1', minHeight: '0', overflow: 'hidden', outline: 'none', '& > *': { height: '100%' } });
const skipCss = css({
  position: 'absolute',
  left: '-9999px',
  top: t.s2,
  zIndex: '10',
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  background: t.accent,
  color: t.onAccent,
  '&:focus': { left: t.s2 },
});

export function Shell(handle: Handle<{ url: URL; children?: RemixNode }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  let dmError: Error | undefined;

  // Ctrl+J toggles the dock, Ctrl+Shift+D opens the diff
  window.addEventListener(
    'keydown',
    (e) => {
      if (e.ctrlKey && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'j') {
        e.preventDefault();
        s.ui.toggleDock();
      } else if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'd') {
        e.preventDefault();
        s.ui.openDock('diff');
      }
    },
    { signal: handle.signal },
  );

  const openDM = async (agentId: string) => {
    const pid = s.workspace.projectId;
    if (!pid) return;
    dmError = undefined;
    try {
      const r = await s.client.roomCreateDM(pid, agentId);
      await navigate(routes.room.href({ id: r.id }));
    } catch (e) {
      dmError = e as Error;
      void handle.update();
    }
  };

  return () => {
    const { url, children } = handle.props;
    const route = parseRoute(url.pathname);
    const ws = s.workspace;
    const project = ws.project;
    const needs = s.project.needsCount;
    handle.queueTask(() => {
      s.ui.setProject(project?.id); // the dock keeps its state per project
      document.title = needs > 0 ? `(${needs}) Drydock` : 'Drydock';
    });

    let center: RemixNode;
    if (ws.projects.loading && !ws.projects.data) {
      center = <Loading />;
    } else if (ws.projects.error && !ws.projects.data) {
      center = (
        <Page title="Cannot reach the Drydock core">
          <ErrorNote error={ws.projects.error} />
          <p>The page needs the core's address and token (open the link the core printed, which ends in ?token=...).</p>
        </Page>
      );
    } else if ((!project || ws.setupOpen) && !isWorkspaceRoute(route) && route.name !== 'project-new') {
      // pages that do not need a project open without one: a read-only install has to reach License and Backups. The
      // first-run wizard stays while it shows what the setup did, until the person opens the new project.
      center = <ProjectCreateScreen first />;
    } else {
      center = children;
    }

    const taskContext =
      route.name === 'task' ? route.id : route.name === 'run' ? route.taskId : route.name === 'room' ? s.project.roomList.find((r) => r.id === route.id)?.taskId : undefined;

    return (
      <div mix={rootCss}>
        <LicenseBanner />
        <div mix={appCss}>
          <a href="#main" mix={skipCss} data-rmx-document>
            Skip to content
          </a>
          <Sidebar route={route} />
          <div mix={centerCss}>
            {dmError ? (
              <div style={{ padding: '8px 16px' }}>
                <ErrorNote error={dmError} what="Could not open the direct message" />
              </div>
            ) : null}
            <main id="main" tabIndex={-1} mix={mainCss}>
              {center}
            </main>
            {project ? <Dock projectId={project.id} taskId={taskContext} /> : null}
          </div>
          {project ? <Details taskId={taskContext} /> : null}
        </div>
        <CommandPalette onMessage={(id) => void openDM(id)} />
      </div>
    );
  };
}
