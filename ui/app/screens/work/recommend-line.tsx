// The line on Work when the crew's advisor has recommendations waiting (decision P52): "<lead> recommends 3 changes · Review",
// linking to the project's Skills page where each one is applied or dismissed by the person. Nothing is applied from here.

import { css, type Handle } from 'remix/component';
import { Lightbulb } from 'lucide';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { recommendsText, waitingRecommendations } from '../skills/recommend.ts';

const lineCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flex: 'none',
  minHeight: '28px',
  margin: '0',
  padding: `${t.s1} ${t.s3}`,
  borderTop: `1px solid ${t.border}`,
  color: t.text,
  fontSize: t.textSm,
  '& a': { fontWeight: '500' },
});

export function RecommendLine(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  watch(handle, s.project);
  // an older core without recommendations reads as none waiting
  const q = query(handle, {
    key: () => handle.props.projectId,
    load: () => s.client.recommendList(handle.props.projectId).catch(() => ({ auto: false, runs: [] })),
    when: (e) => e.type.startsWith('recommend.'),
  });
  return () => {
    const names = new Map(s.project.agentList.map((a) => [a.id, a.name] as const));
    const w = waitingRecommendations(q.data?.runs ?? [], names);
    if (!w) return null;
    return (
      <p mix={lineCss} data-testid="recommend-line">
        <Icon icon={Lightbulb} size={14} />
        <span>{recommendsText(w)}</span>
        <span aria-hidden="true">·</span>
        <a href={routes.skills.href()} data-testid="recommend-line-review">
          Review
        </a>
      </p>
    );
  };
}
