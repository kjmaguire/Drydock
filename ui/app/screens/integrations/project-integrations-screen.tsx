// A project's Integrations page (/project/integrations, in the project sidebar's Project group; decision P51 keeps everything of
// one project off the workspace pages). What the project reaches outside Drydock: the tool calls of its agents waiting for the
// human's approval, the app-sync connections assigned to it (what each imports, its items, rules and outbound writes:
// assignments.tsx), and its agent tools: the MCP servers the workspace has connected, with which of its agents may use each one
// (ProjectServers in screens/mcp/mcp-screen.tsx; one it does not have yet is added from its row), then which tools each role's
// agents get and the project's rules (RoleToolsPanel, role-tools.tsx; decision P115). Services and servers are
// connected once for the workspace, on the workspace's Integrations page; this page only puts them into the project. Laid out
// in the workspace page's rows (decision P98): Services, then Agent tools, each a list of compact rows (logo, name, what this
// project has of it, its state in a word) with the chosen row's panel under the list.

import { css, navigate, on, type Handle } from 'remix/component';
import { Blocks, FolderPlus, Plug, ShieldAlert } from 'lucide';
import type { IntegrationKindInfo, IntegrationView } from '../../api/extTypes.ts';
import type { Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { newProjectHref } from '../../shell/new.ts';
import { query, services, watch } from '../../state/services.tsx';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { emptyLine } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { Approvals, ProjectServers } from '../mcp/mcp-screen.tsx';
import { AssignForm, AssignmentRow, head, type OpenTask } from './assignments.tsx';
import { kindLabel, projectConnState, statusTone } from './integrations.ts';
import { PickRows } from './pick-rows.tsx';
import { RoleToolsPanel } from './role-tools.tsx';
import { plural } from '../../lib/format.ts';

const defaultOpen: OpenTask = (href) => void navigate(href);

const sectionsCss = css({ display: 'grid', gap: t.s8, minWidth: '0' });
// the selected service's panel, right under the tiles (the same card as the workspace page's panel)
const connCss = css({ display: 'grid', gap: t.s3, minWidth: '0', borderColor: t.borderStrong, '& p': { margin: '0' }, '& h3': { fontSize: t.textLg, fontWeight: '600', margin: '0' } });

/** One connected service as this project has it: assigned (its scope, sync, items, rules, outbound) or Assign to <project>. */
function ProjectConnection(handle: Handle<{ conn: IntegrationView; info?: IntegrationKindInfo; project: Project; onChanged: () => void; openTask: OpenTask }>) {
  let assigning = false;
  return () => {
    const { conn, info, project, onChanged, openTask } = handle.props;
    const label = info?.label ?? kindLabel(conn.kind);
    const a = (conn.assignments ?? []).find((x) => x.projectId === project.id);
    const account = conn.identity?.account;
    return (
      <article mix={[card, connCss]} data-testid="project-integration" data-kind={conn.kind} data-assigned={String(!!a)} aria-label={label}>
        <header mix={head}>
          <BrandLogo id={conn.kind} size={32} label />
          <h3>{label}</h3>
          <Badge tone={statusTone(conn.status)}>{conn.status}</Badge>
          {account ? <span mix={muted}>connected as {account}</span> : null}
        </header>
        {a ? (
          <AssignmentRow conn={conn} info={info} a={a} projectName={project.name} onChanged={onChanged} openTask={openTask} />
        ) : assigning ? (
          <AssignForm
            conn={conn}
            info={info}
            projectId={project.id}
            projectName={project.name}
            onDone={() => ((assigning = false), onChanged())}
            onCancel={() => ((assigning = false), void handle.update())}
          />
        ) : (
          <>
            {(conn.legacyItems ?? 0) > 0 ? (
              <Notice tone="info">
                {plural(conn.legacyItems ?? 0, 'item')} {conn.legacyItems === 1 ? 'was' : 'were'} stored before connections and projects were separated; the first project it is assigned to adopts them and the stored settings.
              </Notice>
            ) : null}
            <p mix={muted}>
              Not assigned to {project.name}. Assign it to import {info?.items ?? 'items'} from {label} into this project.
            </p>
            <div>
              <Button size="sm" icon={FolderPlus} mix={[on('click', () => ((assigning = true), void handle.update()))]}>
                Assign to {project.name}
              </Button>
            </div>
          </>
        )}
      </article>
    );
  };
}

export function ProjectIntegrationsScreen(handle: Handle<{ openTask?: OpenTask }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const conns = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  const kinds = query(handle, { load: () => s.client.integrationKinds() });
  // the service whose panel is open: undefined = the first one assigned to this project (else the first), '' = none
  let selected: string | undefined;

  return () => {
    const project = s.workspace.project;
    const openTask = handle.props.openTask ?? defaultOpen;
    if (!project) {
      return (
        <Page title="Integrations" icon={Blocks} testId="project-integrations">
          {s.workspace.projects.data ? (
            <p mix={muted} data-testid="no-project">
              Integrations are put into a project. <a href={newProjectHref()}>Create a project</a> first; services and servers are connected on the{' '}
              <a href={routes.integrations.href()}>Integrations</a> page.
            </p>
          ) : (
            <Loading />
          )}
        </Page>
      );
    }
    const agents = s.project.agentList;
    const list: IntegrationView[] = conns.data ?? [];
    const mine = (c: IntegrationView) => (c.assignments ?? []).some((a) => a.projectId === project.id);
    const current = selected === undefined ? (list.find(mine) ?? list[0]) : list.find((c) => c.id === selected);
    return (
      <Page
        title="Integrations"
        icon={Blocks}
        wide
        guide="integrations"
        subtitle={project.name}
        testId="project-integrations"
        actions={
          <a href={routes.integrations.href()} mix={buttonStyle('ghost', 'sm')} data-testid="workspace-integrations-link">
            <Icon icon={Plug} size={14} />
            Connect services and servers
          </a>
        }
      >
        <div mix={sectionsCss}>
          <Approvals projectId={project.id} agents={agents} />
          <Section
            label="Services"
            testId="project-app-sync"
            title="Services"
            description={`The services connected for the workspace, and what ${project.name} imports from each by app sync, as untrusted text: its scope, items, rules and writes back. Choose a service to open it.`}
          >
            <ErrorNote error={conns.error ?? kinds.error} what="Could not load the services" />
            {conns.loading && !conns.data ? <Loading /> : null}
            {conns.data && list.length === 0 ? (
              <p mix={emptyLine} data-testid="no-connections">
                <Icon icon={ShieldAlert} size={14} />
                <strong>No service is connected yet.</strong> Connect one once on the <a href={routes.integrations.href()}>Integrations</a> page, then assign it here.
              </p>
            ) : null}
            {list.length > 0 ? (
              <PickRows
                label="Connected services"
                selected={current?.id}
                onPick={(id) => ((selected = current?.id === id ? '' : id), void handle.update())}
                rows={list.map((c) => {
                  const info = (kinds.data ?? []).find((k) => k.kind === c.kind);
                  const st = projectConnState(info, c, project.id);
                  return {
                    key: c.id,
                    title: info?.label ?? kindLabel(c.kind),
                    logo: c.kind,
                    sub: st.text,
                    status: st.badge,
                    testId: 'project-integration-tile',
                    data: { kind: c.kind, assigned: String(c.assignments?.some((a) => a.projectId === project.id) ?? false) },
                  };
                })}
              />
            ) : null}
            {current ? (
              <ProjectConnection
                key={current.id}
                conn={current}
                info={(kinds.data ?? []).find((k) => k.kind === current.kind)}
                project={project}
                onChanged={conns.reload}
                openTask={openTask}
              />
            ) : null}
          </Section>
          <ProjectServers projectId={project.id} agents={agents} />
          <RoleToolsPanel key={`roles-${project.id}`} projectId={project.id} />
        </div>
      </Page>
    );
  };
}
