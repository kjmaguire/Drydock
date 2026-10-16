// Home (/home): the workspace outside any project. First a chat with Claude Code that needs no project (decision P60): type
// and start, or open Options for the folder, the agent CLI, skills and MCP servers; the latest chats without a project are
// one click away. Then the projects, each with what needs you there, and everything shared by all of them: the skill library,
// agents and MCP servers, chats, integrations, connections and the app itself. Opening a project card selects that project
// and opens it.

import { css, navigate, on, type Handle } from 'remix/component';
import {
  Cpu,
  SlidersHorizontal,
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
import { action, query, services, watch } from '../../state/services.tsx';
import { Monogram } from '../../shell/project-switcher.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { NewChatDialog } from '../chats/new-chat-dialog.tsx';
import { chatTitle, relTime } from '../chats/chats.ts';
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
const projectGrid = css({ gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: t.s3 });
// a project card: the monogram, the name and where it lives, its crew as a stack of dots, and what needs you there
const projectCard = css({
  alignItems: 'flex-start',
  padding: '18px',
  borderRadius: t.rXl,
  '& > span:last-child': { display: 'grid', gap: '6px' },
  '& strong': { fontSize: t.textLg, letterSpacing: '-0.01em' },
  '& .crew': { display: 'flex', alignItems: 'center', gap: t.s2, marginTop: '4px', color: t.textMuted, fontSize: t.textSm },
  '& .crew .stack': { display: 'flex' },
  '& .crew .stack > *': { marginLeft: '-7px', boxShadow: `0 0 0 2px ${t.bgSurface}` },
  '& .crew .stack > :first-child': { marginLeft: '0' },
  '& small[data-tone]': { marginTop: '4px', padding: '6px 10px', borderRadius: t.rMd, background: t.bgSidebar, fontWeight: '550' },
  '& small[data-tone="warn"]': { background: t.warningSoft, color: t.warning },
  '& small[data-tone="bad"]': { background: t.dangerSoft, color: t.danger },
});
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
function ProjectTile(handle: Handle<{ project: Project; needs: number | null | undefined; crew?: readonly { id: string; name: string }[]; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  const open = (e: MouseEvent) => {
    e.preventDefault();
    s.workspace.selectProject(handle.props.project.id);
    if (handle.props.onNavigate) handle.props.onNavigate(routes.home.href());
    else void navigate(routes.home.href());
  };
  return () => {
    const { project: p, needs, crew } = handle.props;
    const n = needsText(needs);
    return (
      <li>
        <a href={routes.home.href()} mix={[tileCss, linkTile, projectCard, on<HTMLAnchorElement, 'click'>('click', open)]} data-testid="home-project" data-project={p.id} aria-label={`Open ${p.name}`}>
          <Monogram name={p.name} size={40} />
          <span>
            <strong>{p.name}</strong>
            <small style={{ fontFamily: projectHasRepo(p) ? t.mono : undefined }}>{projectWhere(p)}</small>
            {crew && crew.length > 0 ? (
              <span class="crew" aria-hidden="true">
                <span class="stack">
                  {crew.slice(0, 6).map((a) => (
                    <Avatar key={a.id} id={a.id} name={a.name} size={22} kind="agent" />
                  ))}
                </span>
                <span>
                  {crew.length} {crew.length === 1 ? 'agent' : 'agents'}
                </span>
              </span>
            ) : null}
            <small data-tone={n.tone || undefined} data-testid="home-project-needs">
              {n.text}
            </small>
          </span>
        </a>
      </li>
    );
  };
}

const chatForm = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s4} ${t.s4} ${t.s3}`,
  borderRadius: '24px',
  borderColor: t.borderStrong,
  boxShadow: `0 18px 50px -30px ${t.scrim}`,
  '&:focus-within': { borderColor: t.textFaint },
  '& .row': { display: 'flex', alignItems: 'flex-start', gap: t.s3 },
  '& textarea': { width: '100%', minHeight: '72px', resize: 'vertical', padding: '6px 0 0', border: '0', background: 'transparent', fontSize: t.textLg, boxShadow: 'none' },
  '& textarea:focus-visible': { boxShadow: 'none' },
});
const chatBar = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, '& small': { flex: '1', minWidth: '220px', color: t.textMuted, fontSize: t.textSm } });
const recentCss = css({
  display: 'grid',
  gap: '2px',
  margin: `${t.s3} 0 0`,
  padding: '0',
  listStyle: 'none',
  '& a': {
    display: 'flex',
    alignItems: 'baseline',
    gap: t.s2,
    minWidth: '0',
    padding: '6px 8px',
    borderRadius: t.rMd,
    color: t.text,
    textDecoration: 'none',
  },
  '& a:hover': { background: t.bgHover, textDecoration: 'none' },
  '& .title': { fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: '40%' },
  '& .preview': { flex: '1', minWidth: '0', color: t.textMuted, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& time': { color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap' },
});

/**
 * Chat from Home (decision P60): a chat with the person's own signed-in Claude Code CLI that needs no project. Start chat makes
 * the chat in a private folder Drydock creates for it, sends what was typed and opens the chat; Options opens the new chat
 * dialog without a project (folder, agent CLI, model, skills, MCP servers). Below, the latest chats without a project.
 */
function HomeChat(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let text = '';
  let dialog = false;
  let unsent: { id: string; error: Error } | undefined;
  const recent = query(handle, {
    load: async () => (await s.client.chatList({ limit: 100 })).filter((c) => !c.projectId && !c.archived).slice(0, 5),
    when: (e) => e.type === 'chat.created' || e.type === 'chat.updated' || e.type === 'chat.deleted',
  });
  const adapters = query(handle, {
    enabled: () => dialog,
    load: () => s.client.adapterList(),
    when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.'),
  });
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href).catch(() => {}));

  // the chat exists: send what was typed (if anything), then open it; a message that could not be sent stays here with the reason
  const openChat = async (chatId: string) => {
    const msg = text.trim();
    unsent = undefined;
    if (msg) {
      try {
        await s.client.chatSend(chatId, msg);
      } catch (e) {
        unsent = { id: chatId, error: e instanceof Error ? e : new Error(String(e)) };
        void handle.update();
        return;
      }
    }
    text = '';
    go(routes.chat.href({ id: chatId }));
  };
  const start = async () => {
    if (act.busy) return;
    const c = await act.run('start', () => s.client.chatCreate({}));
    if (c) await openChat(c.id);
  };

  return () => {
    const list = recent.data ?? [];
    return (
      <Section
        title="Chat"
        description="Use Claude Code here as you would in a terminal, without a project. It runs your own signed-in Claude Code CLI, on your subscription, and every chat is kept."
        actions={
          <a href={routes.chats.href()} mix={buttonStyle('ghost', 'sm')} data-testid="home-chats-all">
            All chats
          </a>
        }
        testId="home-chat"
      >
        <form
          mix={[
            card,
            chatForm,
            on('submit', (e) => {
              e.preventDefault();
              void start();
            }),
          ]}
          data-testid="home-chat-form"
        >
          <div class="row">
            <Avatar id="claude" name="Claude Code" size={36} kind="agent" />
            <textarea
            aria-label="Message to Claude Code"
            placeholder="Ask Claude Code anything. Ctrl+Enter starts the chat."
            value={text}
            mix={[
              on('input', (e) => ((text = e.currentTarget.value), void handle.update())),
              on('keydown', (e) => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault();
                  void start();
                }
              }),
            ]}
            />
          </div>
          <div mix={chatBar}>
            <small>Runs in a private folder Drydock makes for the chat. A folder of your own, another agent CLI, skills and MCP servers: Options.</small>
            <Button variant="ghost" icon={SlidersHorizontal} data-testid="home-chat-options" mix={[on('click', () => ((dialog = true), void handle.update()))]}>
              Options...
            </Button>
            <Button variant="primary" type="submit" icon={MessagesSquare} disabled={!!act.busy}>
              {act.busy === 'start' ? 'Starting...' : 'Start chat'}
            </Button>
          </div>
          <ErrorNote error={act.error} what="Could not start the chat" />
          {unsent ? (
            <Notice tone="warn" role="alert" testId="home-chat-unsent" title="The chat was made, but your message was not sent.">
              {unsent.error.message} <a href={routes.chat.href({ id: unsent.id })}>Open the chat</a>
            </Notice>
          ) : null}
        </form>
        <ErrorNote error={recent.error} what="Recent chats" />
        {list.length ? (
          <ul mix={recentCss} aria-label="Recent chats without a project">
            {list.map((c) => (
              <li key={c.id}>
                <a href={routes.chat.href({ id: c.id })} data-testid="home-chat-recent" data-chat={c.id}>
                  <span class="title">{chatTitle(c)}</span>
                  <span class="preview">{c.preview}</span>
                  <time>{relTime(c.updatedAt)}</time>
                </a>
              </li>
            ))}
          </ul>
        ) : null}
        <NewChatDialog
          open={dialog}
          onClose={() => ((dialog = false), void handle.update())}
          projects={s.workspace.projectList}
          projectId=""
          adapters={adapters.data}
          adaptersError={adapters.error}
          onCreated={(id) => {
            dialog = false;
            void handle.update();
            void openChat(id);
          }}
        />
      </Section>
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

  // each project's crew, for the dots on its card (a list that cannot be read leaves the card without them)
  const crews = query(handle, {
    load: async () => {
      const list = s.workspace.projectList;
      const read = await Promise.allSettled(list.map((p) => s.client.agentList(p.id)));
      return Object.fromEntries(list.map((p, i) => {
        const r = read[i];
        return [p.id, r?.status === 'fulfilled' ? r.value : []];
      })) as Record<string, { id: string; name: string }[]>;
    },
    key: () => s.workspace.projectList.map((p) => p.id),
    enabled: () => s.workspace.projectList.length > 0,
    when: (e) => e.type.startsWith('agent.'),
  });

  return () => {
    const ws = s.workspace;
    const projects = ws.projectList;
    const hour = new Date().getHours();
    return (
      <Page title={hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening'} icon={House} wide testId="workspace-home" subtitle="A chat, your projects, and everything they share">
        <div mix={sectionsCss}>
          {flash ? (
            <Notice tone={flash.tone} role="status" testId="home-flash">
              {flash.text}
            </Notice>
          ) : null}
          <HomeChat onNavigate={handle.props.onNavigate} />
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
                  <ProjectTile key={p.id} project={p} needs={needs.data?.[p.id]} crew={crews.data?.[p.id]} onNavigate={handle.props.onNavigate} />
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
