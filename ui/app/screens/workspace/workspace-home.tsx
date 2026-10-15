// Home (/home): the workspace outside any project. The projects, each with what needs you there, and everything shared by all
// of them: the skill library, agents and MCP servers, chats, integrations, connections and the app itself. Opening a project
// card selects that project and opens it.

import { css, navigate, on, type Handle } from 'remix/component';
import {
  Cpu,
  Database,
  FolderPlus,
  HardDriveDownload,
  House,
  KeyRound,
  LifeBuoy,
  Library,
  Link2,
  Map,
  MessagesSquare,
  Network,
  Plug,
  Plus,
  Route as RouteIcon,
  ShieldCheck,
} from 'lucide';
import { projectHasRepo, type Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Monogram } from '../../shell/project-switcher.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { ErrorNote, Empty, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { tileCss, tileGrid } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { needsText, projectWhere } from './workspace.ts';

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '86ch' });
const glyph = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flex: 'none',
  width: '32px',
  height: '32px',
  borderRadius: '7px',
  background: t.bgApp,
  color: t.textMuted,
  boxShadow: `inset 0 0 0 1px ${t.border}`,
});
// a tile that is a link: the tile look, without the link underline
const linkTile = css({ textDecoration: 'none', '&:hover': { textDecoration: 'none' } });
const projectGrid = css({ gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))' });
const sectionsCss = css({ display: 'grid', gap: t.s8 });

/** One workspace area as a link tile: an icon, its name and what it holds. */
function AreaTile(handle: Handle<{ href: string; icon: IconNode; title: string; blurb: string; testId: string }>) {
  return () => {
    const { href, icon, title, blurb, testId } = handle.props;
    return (
      <li>
        <a href={href} mix={[tileCss, linkTile]} data-testid={testId}>
          <span mix={glyph} aria-hidden="true">
            <Icon icon={icon} size={16} />
          </span>
          <span>
            <strong>{title}</strong>
            <small>{blurb}</small>
          </span>
        </a>
      </li>
    );
  };
}

/** A project as a link tile: opening it selects the project first, so its pages open under it. */
function ProjectTile(handle: Handle<{ project: Project; needs: number | null | undefined; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  const open = (e: MouseEvent) => {
    e.preventDefault();
    s.workspace.selectProject(handle.props.project.id);
    if (handle.props.onNavigate) handle.props.onNavigate(routes.home.href());
    else void navigate(routes.home.href());
  };
  return () => {
    const { project: p, needs } = handle.props;
    const n = needsText(needs);
    return (
      <li>
        <a href={routes.home.href()} mix={[tileCss, linkTile, on<HTMLAnchorElement, 'click'>('click', open)]} data-testid="home-project" data-project={p.id} aria-label={`Open ${p.name}`}>
          <Monogram name={p.name} size={32} />
          <span>
            <strong>{p.name}</strong>
            <small style={{ fontFamily: projectHasRepo(p) ? t.mono : undefined }}>{projectWhere(p)}</small>
            <small data-tone={n.tone || undefined} data-testid="home-project-needs">
              {n.text}
            </small>
          </span>
        </a>
      </li>
    );
  };
}

export function WorkspaceHomeScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  // a note the previous page left (a project was deleted)
  const flash = s.ui.takeFlash();
  // what needs the human in each project, read per project (a handful of projects; each list is small); a project whose list
  // cannot be read says so on its own tile and the others still show their counts
  const needs = query(handle, {
    load: async () => {
      const list = s.workspace.projectList;
      const read = await Promise.allSettled(list.map((p) => s.client.needsYouList(p.id)));
      return Object.fromEntries(list.map((p, i) => {
        const r = read[i];
        return [p.id, r?.status === 'fulfilled' ? r.value.length : null];
      })) as Record<string, number | null>;
    },
    key: () => s.workspace.projectList.map((p) => p.id),
    enabled: () => s.workspace.projectList.length > 0,
    when: (e) => ['approval.', 'workflow.request', 'task.', 'watchdog.', 'room.message_posted', 'room.archived'].some((p) => e.type.startsWith(p)),
  });

  return () => {
    const ws = s.workspace;
    const projects = ws.projectList;
    return (
      <Page title="Home" icon={House} wide testId="workspace-home" subtitle="Your projects, and everything they share">
        <div mix={sectionsCss}>
          {flash ? (
            <Notice tone={flash.tone} role="status" testId="home-flash">
              {flash.text}
            </Notice>
          ) : null}
          <Section
            title="Projects"
            description="Open a project to work in it: its channels, tasks, team and settings."
            actions={
              <a href={routes.projectNew.href()} mix={buttonStyle('primary', 'sm')} data-testid="home-new-project">
                <Icon icon={Plus} size={14} />
                New project
              </a>
            }
            testId="home-projects"
          >
            <ErrorNote error={ws.projects.error} what="Projects" />
            {ws.projects.loading && !ws.projects.data ? <Loading /> : null}
            {ws.projects.data && projects.length === 0 ? (
              <div mix={card}>
                <Empty title="No projects yet." icon={FolderPlus}>
                  <p>A project is one repository (or an idea without one yet) and the team of agents working on it.</p>
                  <p>
                    <a href={routes.projectNew.href()}>Create your first project</a>
                  </p>
                </Empty>
              </div>
            ) : null}
            {projects.length > 0 ? (
              <ul mix={[tileGrid, projectGrid]} aria-label="Projects">
                {projects.map((p) => (
                  <ProjectTile key={p.id} project={p} needs={needs.data?.[p.id]} onNavigate={handle.props.onNavigate} />
                ))}
              </ul>
            ) : null}
          </Section>

          <Section title="Security" description="Check a repository before you trust it." testId="home-security">
            <ul mix={tileGrid} aria-label="Security">
              <AreaTile href={routes.securityScan.href()} icon={ShieldCheck} title="Security scan" blurb="Scan any folder or project for secrets, vulnerable packages and unsafe code, without running it" testId="home-security-scan" />
            </ul>
          </Section>

          <Section title="Library" description="Defined once for the whole workspace, then given to projects and agents." testId="home-library">
            <ul mix={tileGrid} aria-label="Library">
              <AreaTile href={routes.librarySkills.href()} icon={Cpu} title="Skills" blurb="Instructions agents read, from your skill registries" testId="home-skills" />
              <AreaTile href={routes.library.href()} icon={Library} title="Agents and MCP servers" blurb="Agent templates, prebuilt agents and the MCP servers you connected" testId="home-library-link" />
              <AreaTile href={routes.chats.href()} icon={MessagesSquare} title="Chats" blurb="Your own conversations with an agent, with or without a project" testId="home-chats" />
            </ul>
          </Section>

          <Section title="Connect" description="What Drydock reaches outside itself." testId="home-connect">
            <ul mix={tileGrid} aria-label="Connect">
              <AreaTile href={routes.integrations.href()} icon={Plug} title="Integrations" blurb="GitHub, Linear, Notion, Sentry and agent tools" testId="home-integrations" />
              <AreaTile href={routes.connections.href()} icon={Link2} title="Connections" blurb="The agent CLIs on this computer and in WSL, and the MCP gateway" testId="home-connections" />
              <AreaTile href={routes.providers.href()} icon={Network} title="Providers" blurb="Model providers, keys and prices" testId="home-providers" />
              <AreaTile href={routes.routing.href()} icon={RouteIcon} title="Routing" blurb="Which model each kind of turn uses" testId="home-routing" />
            </ul>
          </Section>

          <Section title="App" testId="home-app">
            <ul mix={tileGrid} aria-label="App">
              <AreaTile href={routes.settings.href()} icon={Map} title="Settings and About" blurb="Theme, updates, telemetry and this build" testId="home-settings" />
              <AreaTile href={routes.backups.href()} icon={HardDriveDownload} title="Backups" blurb="Scheduled copies of Drydock's data" testId="home-backups" />
              <AreaTile href={routes.data.href()} icon={Database} title="Export and import" blurb="Move a project to another computer" testId="home-data" />
              <AreaTile href={routes.license.href()} icon={KeyRound} title="License" blurb="Your license and what it allows" testId="home-license" />
              <AreaTile href={routes.support.href()} icon={LifeBuoy} title="Support bundle" blurb="A redacted bundle for a support request" testId="home-support" />
            </ul>
            <p mix={introCss} style={{ fontSize: t.textSm }}>
              Press Ctrl K to jump to any page.
            </p>
          </Section>
        </div>
      </Page>
    );
  };
}
