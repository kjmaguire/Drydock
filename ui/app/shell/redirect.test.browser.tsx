import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, waitFor } from '../test/render.tsx';
import { Redirect } from './redirect.tsx';

afterEach(cleanup);

describe('Redirect', () => {
  it('opens the page it names once, and says it is opening it', async () => {
    const went: string[] = [];
    const r = mount(<Redirect to="/integrations" go={(h) => went.push(h)} />);
    await waitFor(() => expect(went).toEqual(['/integrations']), r);
    r.root.flush();
    expect(r.container.textContent).toContain('Opening the page');
    // a later render does not send it again
    r.root.flush();
    expect(went).toEqual(['/integrations']);
  });
});
