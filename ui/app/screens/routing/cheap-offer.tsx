// The offer on a cheap, private provider's page (effort review F8): until a cheap model is selected for it, "Probe and select
// now" is one click there, where the provider was just added, instead of a trip to Routing. The probe makes a few small test
// requests, so it runs only on that click.

import { on, type Handle } from 'remix/component';
import type { CheapSelection } from '../../api/extTypes.ts';
import type { Provider } from '../../api/types.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';

export function CheapProbeOffer(handle: Handle<{ p: Provider }>) {
  const s = services(handle);
  const stored = query(handle, {
    load: () => s.client.routeCheapGet(handle.props.p.id),
    key: () => handle.props.p.id,
    enabled: () => offered(handle.props.p),
  });
  const act = action(handle);
  let picked: CheapSelection | undefined;
  return () => {
    const { p } = handle.props;
    if (!offered(p) || !stored.data) return null; // not cheap and private, or not read (an older core): nothing to offer
    if (picked)
      return (
        <p data-testid="cheap-offer-done" role="status">
          Selected for cheap turns: default <code>{picked.default || 'none passed'}</code>, fallback <code>{picked.fallback || 'none'}</code>.
        </p>
      );
    if (stored.data.selected) return null;
    return (
      <Notice tone="info" testId="cheap-offer">
        <span>No cheap model is selected from {p.name} yet. Drydock can test its cheapest few models with a small check; it can cost a little.</span>{' '}
        <Button
          size="sm"
          disabled={act.busy !== ''}
          mix={on('click', async () => {
            const r = await act.run('probe', () => s.client.routeCheapSelect(p.id, false));
            if (r) {
              picked = r;
              void handle.update();
            }
          })}
        >
          {act.busy ? 'Probing...' : 'Probe and select now'}
        </Button>
        <ErrorNote error={act.error} what="Selection failed" />
      </Notice>
    );
  };
}

/** A provider the cheap selection serves: cheap, private and on. */
export const offered = (p: Provider): boolean => p.enabled && p.tier === 'cheap' && p.dataClass === 'private';
