// The line on Work when agents' memory proposals wait for the person (E10): "3 memory proposals · Review", to the Memory page
// where each is accepted or rejected (or all at once). Nothing is accepted from here: an agent proposes, a person decides (P92).

import { css, type Handle } from 'remix/component';
import { Brain } from 'lucide';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { proposalsText } from '../memory/memory.ts';

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

export function MemoryLine(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  // a core that cannot list them reads as none waiting
  const q = query(handle, {
    key: () => handle.props.projectId,
    load: () => s.client.memoryList(handle.props.projectId, 'proposed').catch(() => []),
    when: (e) => e.type.startsWith('memory.'),
  });
  return () => {
    const n = q.data?.length ?? 0;
    if (!n) return null;
    return (
      <p mix={lineCss} data-testid="memory-line">
        <Icon icon={Brain} size={14} />
        <span>{proposalsText(n)}</span>
        <span aria-hidden="true">·</span>
        <a href={routes.memory.href()} data-testid="memory-line-review">
          Review
        </a>
      </p>
    );
  };
}
