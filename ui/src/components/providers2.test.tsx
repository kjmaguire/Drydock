import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { ProviderDetail } from '../api/providerClient';
import { AppServicesProvider } from '../state/context';
import { services } from '../test/fakes';
import { ProviderDetailPanel } from './ProviderDetail';

const withServices = (ui: React.ReactElement, client: Partial<Client>) => render(<AppServicesProvider services={services(client)}>{ui}</AppServicesProvider>);

const detail = (): ProviderDetail => ({
  provider: { id: 'prov_1', name: 'Fixture', kind: 'direct', baseUrl: 'https://api.example.com/v1', keyRef: 'provider/prov_1/api-key', dataClass: 'private', tier: 'cheap', maxConcurrent: 0 } as never,
  models: [{ providerId: 'prov_1', id: 'model-a', inputPerM: 0.5, outputPerM: 1.5, priceSource: 'catalog' } as never],
  quota: [{ kind: 'rpd', model: 'model-a', limit: 250, used: 10, source: 'configured', updatedAt: 1 }],
});

describe('provider detail', () => {
  it('edits settings, a price and a quota limit through the core, and never shows the key', async () => {
    const providerUpdate = vi.fn(async () => ({}) as never);
    const providerSetPrice = vi.fn(async () => {});
    const providerSetQuota = vi.fn(async () => {});
    const { container } = withServices(<ProviderDetailPanel id="prov_1" onChanged={() => {}} />, {
      providerDetail: async () => detail(),
      providerUpdate,
      providerSetPrice,
      providerSetQuota,
    });
    expect(await screen.findByTestId('price-row')).toBeTruthy();
    expect(container.textContent).not.toContain('provider/prov_1/api-key');
    expect(screen.getByTestId('quota-window').textContent).toContain('10 of 250 used');

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Renamed' } });
    fireEvent.change(screen.getByLabelText(/Replace the API key/), { target: { value: 'sk-new' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));
    await waitFor(() => expect(providerUpdate).toHaveBeenCalledWith(expect.objectContaining({ id: 'prov_1', name: 'Renamed', apiKey: 'sk-new', tier: 'cheap' })));

    fireEvent.click(screen.getByRole('button', { name: 'Edit price' }));
    fireEvent.change(screen.getByLabelText('Input price of model-a'), { target: { value: '2' } });
    fireEvent.change(screen.getByLabelText('Output price of model-a'), { target: { value: '6' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save price' }));
    await waitFor(() => expect(providerSetPrice).toHaveBeenCalledWith('prov_1', 'model-a', 2, 6, undefined, undefined));

    fireEvent.change(screen.getByLabelText('Quota window'), { target: { value: 'rpm' } });
    fireEvent.change(screen.getByLabelText('Quota limit'), { target: { value: '30' } });
    fireEvent.click(screen.getByRole('button', { name: 'Set limit' }));
    await waitFor(() => expect(providerSetQuota).toHaveBeenCalledWith('prov_1', '', 'rpm', 30));
  });
});
