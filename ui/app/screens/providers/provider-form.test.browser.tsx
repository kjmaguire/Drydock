import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { NewProvider } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { ProviderForm } from './provider-form.tsx';

afterEach(cleanup);

// TEST DOUBLE: provider.create records what it was sent; nothing reaches a vendor
function show() {
  const calls: NewProvider[] = [];
  const s = testServices({ providerCreate: async (p: NewProvider) => (calls.push(p), null as never), licenseStatus: async () => ({}) as never, appInfo: async () => ({}) as never });
  const r = mount(<ProviderForm onCreated={() => {}} />, s);
  return { r, calls };
}
const has = (root: ParentNode, label: string) => Array.from(root.querySelectorAll('label')).some((l) => l.textContent?.startsWith(label));

describe('Add a provider: pick the vendor, paste the key (A8)', () => {
  it('a vendor chip fills the fields and folds them under Change; the key is all that is typed', async () => {
    const { r, calls } = show();
    const form = await waitFor(() => r.$('form[aria-label="Add a provider"]') as HTMLElement, r);
    // no chip yet: every field, as before; no attestation is asked for before an address needs one
    expect(has(form, 'Provider name')).toBe(true);
    expect(form.querySelector('[data-testid="provider-attestations"]')).toBeNull();
    await r.act(() => (form.querySelector('[data-preset="anthropic"]') as HTMLButtonElement).click());
    const summary = await waitFor(() => r.$('[data-testid="provider-preset-summary"]') as HTMLElement, r);
    expect(summary.textContent).toContain('Anthropic · direct · https://api.anthropic.com/v1 · private · premium');
    expect(has(form, 'Provider name')).toBe(false);
    expect(has(form, 'Base URL')).toBe(false);
    type(byLabel(form, /API key/), 'sk-ant-test');
    await r.act(() => byText(form, 'button', 'Add provider').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({ Name: 'Anthropic', Kind: 'direct', BaseURL: 'https://api.anthropic.com/v1', DataClass: 'private', Tier: 'premium', Attestations: {}, APIKey: 'sk-ant-test' });
  });

  it('Change shows the filled fields; Google AI Studio asks for its billing attestation, and it is the person\'s tick', async () => {
    const { r, calls } = show();
    const form = await waitFor(() => r.$('form[aria-label="Add a provider"]') as HTMLElement, r);
    await r.act(() => (form.querySelector('[data-preset="gemini"]') as HTMLButtonElement).click());
    const att = await waitFor(() => r.$('[data-testid="provider-attestations"]') as HTMLElement, r);
    expect(att.textContent).toContain('billing enabled');
    expect(att.textContent).not.toContain('plain http');
    expect((att.querySelector('input[type="checkbox"]') as HTMLInputElement).checked).toBe(false);
    await r.act(() => byText(form, 'button', 'Change').click());
    await waitFor(() => expect((byLabel<HTMLInputElement>(form, 'Base URL')).value).toBe('https://generativelanguage.googleapis.com/v1beta/openai'), r);
    expect(byLabel<HTMLInputElement>(form, 'Provider name').value).toBe('Google AI Studio');
    await r.act(() => byText(form, 'button', 'Add provider').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ Name: 'Google AI Studio', DataClass: 'trainable', Tier: 'free', Attestations: {} });
  });

  it('a known vendor address typed in fills the rest; an attestation ticked for another address is not sent', async () => {
    const { r, calls } = show();
    const form = await waitFor(() => r.$('form[aria-label="Add a provider"]') as HTMLElement, r);
    await r.act(() => type(byLabel(form, 'Base URL'), 'http://192.168.1.5:8000/v1'));
    await r.act(() => byLabel(form, /accept plain http/).click());
    await r.act(() => type(byLabel(form, 'Base URL'), 'https://api.groq.com/openai/v1'));
    await waitFor(() => expect(byLabel<HTMLInputElement>(form, 'Provider name').value).toBe('Groq'), r);
    expect(form.querySelector('[data-testid="provider-attestations"]')).toBeNull();
    await r.act(() => byText(form, 'button', 'Add provider').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({ Name: 'Groq', Kind: 'direct', BaseURL: 'https://api.groq.com/openai/v1', DataClass: 'private', Tier: 'cheap', Attestations: {} });
  });
});
