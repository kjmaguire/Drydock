import { css, type Handle } from 'remix/component';
import { BookOpen } from 'lucide';
import { guideTopic, type GuideId } from '../screens/guide/topics.ts';
import { topicPath } from '../screens/guide/model.ts';
import { Icon } from './icon.tsx';
import { t } from './tokens.ts';

const linkCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '5px',
  minHeight: '28px',
  color: t.accentText,
  fontSize: t.textSm,
  fontWeight: '500',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  '&:hover': { textDecoration: 'underline' },
});

/**
 * A link to a Guide topic: where a page sends the explanation it leaves out (the UI audit of 2026-10-19). The topic is named by
 * its id, so a typo does not compile; its title is the link's accessible name, because "Learn more" alone says nothing out of context.
 */
export function LearnMore(handle: Handle<{ topic: GuideId; label?: string }>) {
  return () => {
    const { topic, label = 'Learn more' } = handle.props;
    const title = guideTopic(topic)?.title ?? topic;
    return (
      <a href={topicPath(topic)} mix={linkCss} aria-label={`${label}: ${title}`} title={title} data-testid="learn-more" data-topic={topic}>
        <Icon icon={BookOpen} size={13} />
        {label}
      </a>
    );
  };
}
