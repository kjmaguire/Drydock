// The Knowledge tab (decision P64): what the crew knows about the project and where it came from. One card per source,
// each a link to its page under the hub's pills: requirements, expert packs, skills, memory and the repo map.

import { css, type Handle } from 'remix/component';
import { BookOpen, Brain, Cpu, FileText, FolderTree, type IconNode } from 'lucide';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';

const gridCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });
const srcCss = css({
  display: 'grid',
  gap: '6px',
  padding: '18px',
  borderRadius: t.rXl,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  color: t.text,
  textDecoration: 'none',
  transition: `border-color ${t.fast} ${t.ease}, transform ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, textDecoration: 'none', transform: 'translateY(-1px)' },
  '& .ic': { display: 'grid', placeItems: 'center', width: '40px', height: '40px', marginBottom: t.s2, borderRadius: '12px' },
  '& b': { fontSize: t.textLg, fontWeight: '650', letterSpacing: '-0.01em' },
  '& small': { color: t.textMuted, fontSize: t.textSm, lineHeight: '1.45' },
});

interface Source {
  href: string;
  icon: IconNode;
  title: string;
  blurb: string;
  tone: string;
  testId: string;
}

const SOURCES: Source[] = [
  { href: routes.requirements.href(), icon: FileText, title: 'Requirements', blurb: 'What the project must do, and which task covers each one', tone: 'var(--info-soft)', testId: 'knowledge-requirements' },
  { href: routes.packs.href(), icon: BookOpen, title: 'Expert packs', blurb: 'Reference material the crew reads: APIs, style, the domain', tone: 'var(--danger-soft)', testId: 'knowledge-packs' },
  { href: routes.skills.href(), icon: Cpu, title: 'Skills', blurb: 'Instructions agents follow for a kind of work, and who gets them', tone: 'var(--warning-soft)', testId: 'knowledge-skills' },
  { href: routes.memory.href(), icon: Brain, title: 'Memory', blurb: 'Notes the crew wrote down for next time', tone: 'var(--accent-soft)', testId: 'knowledge-memory' },
  { href: routes.repoMap.href(), icon: FolderTree, title: 'Repo map', blurb: 'The most important code, ranked, so an agent starts in the right place', tone: 'var(--success-soft)', testId: 'knowledge-repo-map' },
];

export function KnowledgeScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  return () => (
    <Page title="Knowledge" icon={BookOpen} subtitle={`What the crew knows about ${s.workspace.project?.name ?? 'the project'}, and where it came from`} testId="knowledge">
      <ul mix={gridCss} aria-label="Sources">
        {SOURCES.map((x) => (
          <li key={x.testId}>
            <a href={x.href} mix={srcCss} data-testid={x.testId}>
              <span class="ic" style={{ background: x.tone }}>
                <Icon icon={x.icon} size={18} />
              </span>
              <b>{x.title}</b>
              <small>{x.blurb}</small>
            </a>
          </li>
        ))}
      </ul>
    </Page>
  );
}
