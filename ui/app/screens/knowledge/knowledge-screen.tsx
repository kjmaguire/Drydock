// The Knowledge tab (decision P64): what the crew knows about the project and where it came from. A question box that
// starts a Claude Code chat in the project's folder, one card per source with its real count (requirements, expert packs,
// the repo map, memory, the decisions room), the requirements with how far each is covered by a task, and the latest memory
// notes with who wrote them. Every number is a projection of a core read; when the core has no field for something, the
// hub does not show it.

import { css, navigate, on, type Handle } from 'remix/component';
import { ArrowUp, BookOpen, Brain, FileText, Flag, Layers, Sparkles, type IconNode } from 'lucide';
import { projectHasRepo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Avatar, Badge } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, card } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { relTime } from '../chats/chats.ts';
import { kindLabel } from '../memory/memory.ts';
import { languagesText } from '../repomap/repomap.ts';
import { authorOf, countText, coverageOf, coverageText, decisionMessages, latestNotes, liveTraces, memoryBlurb, packsBlurb, requirementsBlurb } from './knowledge.ts';

const DECISIONS_PAGE = 200;
const NOTES = 5;

const askCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: '12px 14px 12px 18px',
  borderRadius: t.rXl,
  border: `1px solid ${t.borderStrong}`,
  background: t.bgSurface,
  boxShadow: t.shadowMd,
  '& > svg': { flex: 'none', color: t.textMuted },
  '& input': { flex: '1', minWidth: '0', height: '36px', padding: '0', border: '0', background: 'transparent', fontSize: t.textLg, color: t.text, boxShadow: 'none' },
  '& input:focus': { outline: 'none', boxShadow: 'none' },
  '& input::placeholder': { color: t.textFaint },
  '& button': {
    display: 'grid',
    placeItems: 'center',
    flex: 'none',
    width: '38px',
    height: '38px',
    minHeight: '0',
    padding: '0',
    border: '0',
    borderRadius: '50%',
    background: t.accent,
    color: t.onAccent,
  },
  '& button:hover': { background: t.accentHover },
  '& button:disabled': { opacity: '0.5' },
  '& button:focus-visible': { boxShadow: t.focus, outline: 'none' },
});
const askNote = css({ margin: `${t.s2} 0 0`, padding: `0 ${t.s4}`, color: t.textFaint, fontSize: t.textXs });

const sourcesCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });
const srcCss = css({
  display: 'grid',
  gap: '4px',
  alignContent: 'start',
  height: '100%',
  padding: '16px',
  borderRadius: t.rXl,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  color: t.text,
  textDecoration: 'none',
  transition: `border-color ${t.fast} ${t.ease}, transform ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, textDecoration: 'none', transform: 'translateY(-1px)' },
  '& .ic': { display: 'grid', placeItems: 'center', width: '38px', height: '38px', marginBottom: t.s2, borderRadius: t.rLg },
  '& .n': { fontSize: t.text2xl, fontWeight: '700', letterSpacing: '-0.02em', lineHeight: '1.1', fontVariantNumeric: 'tabular-nums' },
  '& .n[data-empty]': { color: t.textFaint },
  '& b': { fontSize: t.textLg, fontWeight: '600', letterSpacing: '-0.01em' },
  '& small': { color: t.textMuted, fontSize: t.textSm, lineHeight: '1.45', overflowWrap: 'anywhere' },
});

const twoCss = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1.3fr) minmax(0, 1fr)', gap: t.s4, alignItems: 'start', '@media (max-width: 1000px)': { gridTemplateColumns: 'minmax(0, 1fr)' } });
const panelCss = css({ borderRadius: t.rXl });
const panelTitle = css({
  display: 'flex',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: t.s2,
  margin: `0 0 ${t.s2}`,
  fontSize: t.textMd,
  fontWeight: '600',
  '& a': { display: 'inline-flex', alignItems: 'center', minHeight: '28px', color: t.textFaint, fontSize: t.textSm, fontWeight: '500' },
  '& a:hover': { color: t.text },
});
const reqList = css({ margin: '0', padding: '0', listStyle: 'none' });
const reqRow = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr) auto',
  gap: t.s3,
  alignItems: 'center',
  padding: '11px 4px',
  borderTop: `1px solid ${t.border}`,
  fontSize: t.textMd,
  '&:first-child': { borderTop: '0' },
  '& .id': { fontFamily: t.mono, fontSize: t.textXs, color: t.textFaint },
  '& .title': { color: t.text, textDecoration: 'none', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .title:hover': { textDecoration: 'underline' },
});
const chipCss = css({ display: 'inline-flex', alignItems: 'center', padding: '3px 9px', borderRadius: t.rFull, fontSize: t.textXs, fontWeight: '600', whiteSpace: 'nowrap', textDecoration: 'none', '&:hover': { textDecoration: 'none', filter: 'brightness(0.96)' } });
const noteList = css({ margin: '0', padding: '0', listStyle: 'none' });
const noteRow = css({
  display: 'grid',
  gridTemplateColumns: '28px minmax(0, 1fr)',
  gap: '10px',
  alignItems: 'start',
  padding: '10px 0',
  borderTop: `1px solid ${t.border}`,
  fontSize: t.textSm,
  lineHeight: '1.5',
  '&:first-child': { borderTop: '0' },
  '& .who': { display: 'grid', placeItems: 'center', height: '26px', width: '28px' },
  '& .who > span[data-other]': { width: '26px', height: '26px', borderRadius: '50%', background: t.bgActive },
  '& .text': { color: t.text, textDecoration: 'none', overflowWrap: 'anywhere' },
  '& .text:hover': { textDecoration: 'underline' },
  '& small': { display: 'block', marginTop: '2px', color: t.textFaint, fontSize: t.textXs },
});
const quietCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd });

function SourceCard(handle: Handle<{ href: string; icon: IconNode; title: string; count?: string; blurb: string; tint: string; testId: string }>) {
  return () => {
    const { href, icon, title, count, blurb, tint, testId } = handle.props;
    return (
      <li>
        <a href={href} mix={srcCss} data-testid={testId} data-count={count}>
          <span class="ic" style={{ background: tint }}>
            <Icon icon={icon} size={18} />
          </span>
          <span class="n" data-empty={count === undefined ? '' : undefined} aria-hidden={count === undefined ? 'true' : undefined}>
            {count ?? '–'}
          </span>
          <b>{title}</b>
          <small>{blurb}</small>
        </a>
      </li>
    );
  };
}

export function KnowledgeScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  let question = '';
  let unsent: { id: string; error: Error } | undefined;

  const coverage = query(handle, {
    load: () => s.client.requirementCoverage(pid()!),
    when: (e) => e.type.startsWith('requirement.') || e.type.startsWith('task.'),
    key: pid,
    enabled: () => !!pid(),
  });
  const packs = query(handle, {
    load: () => s.client.packList(),
    when: (e) => e.type.startsWith('pack.'),
    key: pid,
    enabled: () => !!pid(),
  });
  const memory = query(handle, {
    load: () => s.client.memoryList(pid()!),
    when: (e) => e.type.startsWith('memory.'),
    key: pid,
    enabled: () => !!pid(),
  });
  const repoMap = query(handle, {
    load: () => s.client.repoMapGet(pid()!),
    key: pid,
    enabled: () => !!pid() && projectHasRepo(s.workspace.project),
  });
  const decisionsRoom = () => s.project.roomList.find((r) => r.kind === 'channel' && r.name === 'decisions' && !r.archived);
  const decisions = query(handle, {
    load: () => s.client.roomMessagesLatest(decisionsRoom()!.id, DECISIONS_PAGE),
    when: (e) => e.type === 'room.message_posted',
    key: () => decisionsRoom()?.id,
    enabled: () => !!decisionsRoom(),
  });

  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href).catch(() => {}));
  // the chat exists: send the question, then open it; a question that could not be sent stays here with the reason
  // `busy` is held from the click until the question has been sent (or has failed to send), not only until the chat exists:
  // sending is a paid turn, and a second Enter or click meanwhile would start a second chat and send it again (audit L40)
  const ask = async () => {
    const projectId = pid();
    const text = question.trim();
    if (!projectId || !text || act.busy) return;
    unsent = undefined;
    await act.run('ask', async () => {
      const c = await s.client.chatCreate({ projectId });
      try {
        await s.client.chatSend(c.id, text);
      } catch (e) {
        unsent = { id: c.id, error: e instanceof Error ? e : new Error(String(e)) };
        return;
      }
      question = '';
      void handle.update();
      go(routes.chat.href({ id: c.id }));
    });
  };

  return () => {
    const project = s.workspace.project;
    const name = project?.name ?? 'the project';
    const tasks = s.project.taskList;
    const agents = s.project.agentList;
    const traces = liveTraces(coverage.data ?? []);
    const packList = packs.data ?? [];
    const notes = memory.data ?? [];
    const map = repoMap.data;
    const room = decisionsRoom();
    const decided = decisions.data;
    return (
      <Page title="Knowledge" icon={BookOpen} subtitle={`What the crew knows about ${name}, and where it came from`} testId="knowledge">
        <div>
          <form
            mix={[
              askCss,
              on('submit', (e) => {
                e.preventDefault();
                void ask();
              }),
            ]}
            data-testid="knowledge-ask"
          >
            <Icon icon={Sparkles} size={20} />
            <input
              type="text"
              aria-label={`Ask anything about ${name}`}
              placeholder={`Ask anything about ${name}`}
              value={question}
              disabled={!project}
              mix={on('input', (e) => ((question = e.currentTarget.value), void handle.update()))}
            />
            <button type="submit" aria-label="Ask" disabled={!project || !!act.busy}>
              <Icon icon={ArrowUp} size={18} />
            </button>
          </form>
          <p mix={askNote}>Starts a Claude Code chat in the project's folder: your own Claude Code CLI reads the code and answers there.</p>
          <ErrorNote error={act.error} what="Could not start the chat" />
          {unsent ? (
            <Notice tone="warn" role="alert" testId="knowledge-ask-unsent" title="The chat was made, but your question was not sent.">
              {unsent.error.message} <a href={routes.chat.href({ id: unsent.id })}>Open the chat</a>
            </Notice>
          ) : null}
        </div>

        <ul mix={sourcesCss} aria-label="Sources">
          <SourceCard href={routes.requirements.href()} icon={FileText} title="Requirements" tint={t.infoSoft} testId="knowledge-requirements" count={coverage.data ? String(traces.length) : undefined} blurb={coverage.error ? 'could not be read' : requirementsBlurb(traces)} />
          <SourceCard href={routes.packs.href()} icon={BookOpen} title="Expert packs" tint={t.dangerSoft} testId="knowledge-packs" count={packs.data ? String(packList.length) : undefined} blurb={packs.error ? 'could not be read' : packsBlurb(packList)} />
          <SourceCard
            href={routes.repoMap.href()}
            icon={Layers}
            title="Repo map"
            tint={t.successSoft}
            testId="knowledge-repo-map"
            count={map?.result ? String(map.result.files) : undefined}
            blurb={repoMap.error ? 'could not be read' : map?.result ? `files mapped · ${languagesText(map.result.languages) || 'no language recognised'}` : map ? map.message || 'no map yet' : projectHasRepo(project) ? 'the code, ranked, so an agent starts in the right place' : 'needs a repository'}
          />
          <SourceCard href={routes.memory.href()} icon={Brain} title="Memory" tint={t.accentSoft} testId="knowledge-memory" count={memory.data ? String(notes.length) : undefined} blurb={memory.error ? 'could not be read' : memoryBlurb(notes)} />
          <SourceCard
            href={room ? routes.room.href({ id: room.id }) : routes.rooms.href()}
            icon={Flag}
            title="Decisions"
            tint={t.warningSoft}
            testId="knowledge-decisions"
            count={decided ? countText(decisionMessages(decided), decided.length >= DECISIONS_PAGE) : undefined}
            blurb={decisions.error ? 'could not be read' : room ? 'agreed in decisions' : 'the decisions room'}
          />
        </ul>

        <div mix={twoCss}>
          <section mix={[card, panelCss]} aria-labelledby="knowledge-requirements-title" data-testid="knowledge-requirements-panel">
            <h2 mix={panelTitle} id="knowledge-requirements-title">
              Requirements <a href={routes.requirements.href()}>All requirements</a>
            </h2>
            <ErrorNote error={coverage.error} what="Could not read the requirements" />
            {coverage.data && traces.length === 0 ? (
              <p mix={quietCss}>
                No requirements yet. <a href={routes.requirements.href()}>Add one</a> and link it to the tasks that cover it.
              </p>
            ) : (
              <ul mix={reqList}>
                {traces.map((tr, i) => {
                  const c = coverageOf(tr);
                  const col = tone[c.tone];
                  const chip = coverageText(c, tasks);
                  return (
                    <li key={tr.requirement.id} mix={reqRow} data-testid="knowledge-requirement" data-coverage={c.label}>
                      <span class="id" aria-hidden="true">
                        R-{i + 1}
                      </span>
                      <a class="title" href={routes.requirements.href()} title={tr.requirement.title}>
                        {tr.requirement.title}
                      </a>
                      {c.taskId ? (
                        <a href={routes.task.href({ id: c.taskId })} mix={chipCss} style={{ color: col.fg, background: col.bg }} data-testid="knowledge-coverage">
                          {chip}
                        </a>
                      ) : (
                        <span mix={chipCss} style={{ color: col.fg, background: col.bg }} data-testid="knowledge-coverage">
                          {chip}
                        </span>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </section>

          <section mix={[card, panelCss]} aria-labelledby="knowledge-learned-title" data-testid="knowledge-learned">
            <h2 mix={panelTitle} id="knowledge-learned-title">
              Recently learned <a href={routes.memory.href()}>memory</a>
            </h2>
            <ErrorNote error={memory.error} what="Could not read the memory" />
            {memory.data && notes.length === 0 ? (
              <p mix={quietCss}>Nothing yet. Notes the crew proposes land here; agents read the ones you accept.</p>
            ) : (
              <ul mix={noteList}>
                {latestNotes(notes, NOTES).map((e) => {
                  const who = authorOf(e.source, agents);
                  const meta = [who?.name, kindLabel(e.kind), relTime(e.updated)].filter(Boolean).join(' · ');
                  return (
                    <li key={e.id} mix={noteRow} data-testid="knowledge-note" data-status={e.status}>
                      <span class="who">
                        {who?.kind === 'agent' ? <Avatar id={who.id} name={who.name} size={26} kind="agent" /> : who?.kind === 'human' ? <Avatar id={who.id} name={who.name} size={26} kind="human" /> : <span data-other="" aria-hidden="true" />}
                      </span>
                      <div>
                        <a class="text" href={routes.memory.href()}>
                          {e.title}
                        </a>
                        {e.status !== 'accepted' ? (
                          <>
                            {' '}
                            <Badge tone={e.status === 'proposed' ? 'warn' : 'muted'}>{e.status}</Badge>
                          </>
                        ) : null}
                        <small>{meta}</small>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        </div>
      </Page>
    );
  };
}
