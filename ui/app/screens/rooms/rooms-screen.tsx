// The Rooms tab (decision P64): opens the project's general channel (else its first channel) as soon as the rooms are
// known; the Rooms rail beside it lists every room. A project without a channel yet says so.

import { navigate, type Handle } from 'remix/component';
import { MessagesSquare } from 'lucide';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { Empty, Loading } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';

export function RoomsScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.project);
  let sent = false;
  return () => {
    const rooms = s.project.rooms;
    const channels = s.project.roomList.filter((r) => r.kind === 'channel' && !r.archived);
    const first = channels.find((r) => r.name === 'general') ?? channels[0];
    if (first && !sent) {
      sent = true;
      handle.queueTask(() => void navigate(routes.room.href({ id: first.id }), { history: 'replace' }));
    }
    if (first || (!rooms.data && rooms.loading)) return <Loading label="Opening the project channel..." />;
    return (
      <Page title="Rooms" icon={MessagesSquare} subtitle="Where you and the crew talk">
        <Empty title="No rooms yet" icon={MessagesSquare}>
          <p>A project gets its channels (general, decisions, alerts, reviews) when it is created; each task opens a room of its own.</p>
        </Empty>
      </Page>
    );
  };
}
