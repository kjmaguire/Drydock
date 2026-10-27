import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { NoticeState } from '../api/pkgTypes.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { TermsNotice } from './terms-notice.tsx';

afterEach(cleanup);

const notice = (over: Partial<NoticeState> = {}): NoticeState => ({ id: 'claude-terms', version: 1, acknowledged: false, ...over });

describe('TermsNotice (decision D1)', () => {
  it('says the agents run as the person under their own terms, links the terms, and goes once acknowledged', async () => {
    let state = notice();
    const acks: [string, number][] = [];
    const r = mount(
      <TermsNotice />,
      testServices({
        noticeStatus: async () => ({ notices: [state] }),
        noticeAcknowledge: async (id, version) => (acks.push([id, version]), (state = notice({ acknowledged: true, at: 1 })), state),
      }),
    );
    const el = await waitFor(() => {
      const n = r.container.querySelector<HTMLElement>('[data-testid="terms-notice"]');
      if (!n) throw new Error('no notice');
      return n;
    }, r);
    expect(el.textContent).toContain('signed in with your own Anthropic account');
    expect(el.textContent).toContain('never reads, copies or sends your Claude credentials');
    const links = Array.from(el.querySelectorAll('a')).map((a) => a.getAttribute('href'));
    expect(links).toEqual(['https://www.anthropic.com/legal/consumer-terms', 'https://www.anthropic.com/legal/commercial-terms', 'https://www.anthropic.com/legal/aup']);
    // it has no close button: only the acknowledgement takes it away
    expect(el.querySelectorAll('button').length).toBe(1);
    await r.act(() => byText<HTMLButtonElement>(el, 'button', 'I understand').click());
    await waitFor(() => {
      if (r.container.querySelector('[data-testid="terms-notice"]')) throw new Error('still shown');
    }, r);
    expect(acks).toEqual([['claude-terms', 1]]);
  });

  it('draws nothing once acknowledged, and nothing when the core cannot answer', async () => {
    const a = mount(<TermsNotice />, testServices({ noticeStatus: async () => ({ notices: [notice({ acknowledged: true })] }) }));
    await a.act(() => undefined);
    expect(a.container.querySelector('[data-testid="terms-notice"]')).toBe(null);
    const b = mount(<TermsNotice />, testServices({ noticeStatus: async () => Promise.reject(new Error('down')) }));
    await b.act(() => undefined);
    expect(b.container.querySelector('[data-testid="terms-notice"]')).toBe(null);
    expect(b.container.textContent).not.toContain('down');
  });
});
