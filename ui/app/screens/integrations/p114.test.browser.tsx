import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AutoApproveClass, IntegrationAttachment, WebhookStatus } from '../../api/extTypes.ts';
import { byLabel, byText, choose, cleanup, errorOf, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { BackgroundPanel } from './background-panel.tsx';
import { asg, conn, item } from './fixtures.ts';
import { AutoApprove, ItemAttachments, Mappings, PropsTable, StatusSuggestionNote } from './project-extras.tsx';

afterEach(cleanup);

// TEST DOUBLES only: partial clients answering with the shapes internal/integrations returns for decision P114 (the webhook
// receiver, the weekly sweep, mappings, auto-approve classes, attachments, typed rows, the status a service reports).

type Calls = unknown[][];
const rec =
  <R,>(calls: Calls, result: (...args: unknown[]) => R) =>
  async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };

/** The element a selector names, once it is drawn. */
const need = (r: ReturnType<typeof mount>, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);

const hooks = (over: Partial<WebhookStatus> = {}): WebhookStatus => ({
  enabled: false,
  port: 0,
  listening: false,
  endpoints: [
    { kind: 'linear', label: 'Linear', path: '/hooks/linear', integrationId: 'int_1', hasSecret: false, secret: 'signing secret', header: 'Linear-Signature', where: 'Linear: Settings > API > Webhooks.' },
    { kind: 'sentry', label: 'Sentry', path: '/hooks/sentry', hasSecret: false, secret: 'client secret', header: 'Sentry-Hook-Signature', where: 'Sentry: Developer Settings.' },
    { kind: 'notion', label: 'Notion', path: '/hooks/notion', hasSecret: false, secret: 'verification token', header: 'X-Notion-Signature', where: 'Notion: Webhooks tab.' },
  ],
  queued: 0,
  recent: [],
  exposeNote: 'Drydock listens only on this computer (127.0.0.1). Linear, Sentry and Notion cannot reach it until you expose this port yourself, for example with your own tunnel; Drydock does not do that for you.',
  ...over,
});

describe('Webhooks and sweeps (the workspace page)', () => {
  it('is off by default, says plainly that the person exposes the port, checks the port, and keeps each secret write-only', async () => {
    const calls: Calls = [];
    let st = hooks();
    const s = testServices({
      integrationWebhooks: async () => st,
      integrationWebhooksSet: rec(calls, (req) => (st = hooks({ ...(req as object), listening: !!(req as { enabled?: boolean }).enabled, address: 'http://127.0.0.1:8787', port: 8787 }))),
      integrationWebhookSecret: rec(calls, () => (st = hooks({ ...st, endpoints: st.endpoints!.map((e) => (e.kind === 'linear' ? { ...e, hasSecret: true } : e)) }))),
      integrationSweep: async () => ({ enabled: true, everyMs: 604800000 }),
      integrationSweepSet: rec(calls, (v) => ({ enabled: v as boolean, everyMs: 604800000 })),
    } as Partial<Client>);
    const r = mount(<BackgroundPanel />, s);
    const expose = await need(r, '[data-testid="webhook-expose"]');
    expect(expose.textContent).toContain('Drydock does not do that for you');
    expect(r.$('[data-testid="webhook-state"]')!.getAttribute('data-listening')).toBe('false');
    expect(r.$('[data-testid="background-head"]')!.textContent).toContain('Off');
    // turning it on without a port says so, and sends nothing
    const sw = byLabel(r.container, 'Receive webhooks') as unknown as HTMLButtonElement;
    await r.act(() => sw.click());
    const portBox = byLabel(r.container, 'Port on 127.0.0.1 (1024 to 65535)');
    await waitFor(() => expect(errorOf(portBox)).toBe('Choose the port first.'), r);
    expect(calls.length).toBe(0);
    type(portBox, '80');
    await r.act(() => byText(r.container, 'button', 'Save port').click());
    await waitFor(() => expect(errorOf(byLabel(r.container, 'Port on 127.0.0.1 (1024 to 65535)'))).toBe('Choose a port between 1024 and 65535.'), r);
    expect(calls.length).toBe(0);
    type(byLabel(r.container, 'Port on 127.0.0.1 (1024 to 65535)'), '8787');
    await r.act(() => (byLabel(r.container, 'Receive webhooks') as unknown as HTMLButtonElement).click());
    await waitFor(() => expect(calls[0]).toEqual([{ enabled: true, port: 8787 }]), r);
    await waitFor(() => expect(r.$('[data-testid="webhook-state"]')!.textContent).toContain('http://127.0.0.1:8787'), r);
    // a connected vendor takes its secret; a vendor that is not connected says so
    const eps = Array.from(r.$$('[data-testid="webhook-endpoint"]')) as HTMLElement[];
    expect(eps.length).toBe(3);
    expect(eps[1]!.textContent).toContain('Connect Sentry first.');
    const secretBox = byLabel(eps[0]!, 'Linear signing secret');
    expect(secretBox.getAttribute('type')).toBe('password');
    await r.act(() => type(secretBox, 'lin_wh_secret_value_123'));
    await r.act(() => byText(eps[0]!, 'button', 'Save secret').click());
    await waitFor(() => expect(calls[1]).toEqual(['int_1', 'lin_wh_secret_value_123']), r);
    await waitFor(() => expect(r.$('[data-kind="linear"][data-has-secret]')!.getAttribute('data-has-secret')).toBe('true'), r);
    expect(r.container.textContent).not.toContain('lin_wh_secret_value_123');
    // the weekly sweep can be turned off
    await r.act(() => (byLabel(r.container, 'Sweep every week') as unknown as HTMLButtonElement).click());
    await waitFor(() => expect(calls.some((c) => c[0] === false)).toBe(true), r);
    await waitFor(() => expect(r.$('[data-testid="sweep-state"]')!.getAttribute('data-enabled')).toBe('false'), r);
  });

  it("offers Notion's verification token and uses it only when the person says so", async () => {
    const calls: Calls = [];
    const s = testServices({
      integrationWebhooks: async () => hooks({ enabled: true, port: 8787, listening: true, address: 'http://127.0.0.1:8787', notionVerification: { token: 'secret_abc123token', receivedAt: 1 } }),
      integrationUseNotionToken: rec(calls, () => hooks({ enabled: true, port: 8787, listening: true })),
      integrationSweep: async () => ({ enabled: true, everyMs: 1 }),
    } as Partial<Client>);
    const r = mount(<BackgroundPanel />, s);
    const note = await need(r, '[data-testid="notion-verification"]');
    expect(note.textContent).toContain('secret_abc123token');
    expect(calls.length).toBe(0);
    await r.act(() => byText(note, 'button', 'Use this token').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    await waitFor(() => expect(r.$('[data-testid="notion-verification"]')).toBeNull(), r);
  });
});

describe("a project's assignment: mappings, auto-approve, attachments, typed rows (decision P114)", () => {
  it('saves status mappings between the service and task statuses', async () => {
    const calls: Calls = [];
    const s = testServices({
      integrationMappings: async () => [{ scope: 'status', remote: 'Done', local: 'COMPLETED', direction: 'to_vendor' }],
      integrationMappingFields: async () => [{ name: 'Todo' }, { name: 'In Progress' }, { name: 'Done' }],
      integrationMappingsSet: rec(calls, (_a, _s, ms) => ms as never),
    } as Partial<Client>);
    const r = mount(<Mappings conn={conn()} a={asg()} />, s);
    await waitFor(() => expect(r.$$('[data-testid="status-mapping"]').length).toBe(1), r);
    await r.act(() => byText(r.container, 'button', "List Linear's statuses").click());
    await waitFor(() => expect(r.$$('datalist option').length).toBe(3), r);
    await r.act(() => byText(r.container, 'button', 'Add a status').click());
    const rows = Array.from(r.$$('[data-testid="status-mapping"]')) as HTMLElement[];
    type(byLabel(rows[1]!, 'Linear status'), 'In Progress');
    choose(byLabel(rows[1]!, 'Task status') as HTMLElement, 'IN_PROGRESS');
    choose(byLabel(rows[1]!, 'Direction') as HTMLElement, 'both');
    await r.act(() => byText(r.container, 'button', 'Save status mappings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual([
      'asg_1',
      'status',
      [
        { remote: 'Done', local: 'COMPLETED', direction: 'to_vendor' },
        { remote: 'In Progress', local: 'IN_PROGRESS', direction: 'both' },
      ],
    ]);
    // only Notion has typed rows to map
    expect(r.$$('[data-testid="property-mapping"]').length).toBe(0);
  });

  it('auto-approve classes are off until the person turns one on, with its version', async () => {
    const calls: Calls = [];
    let classes: AutoApproveClass[] = [
      { integrationId: 'int_1', projectId: 'prj_1', class: 'linkback_comment', label: 'Pull request link-back comment', description: 'The fixed comment.', enabled: false, version: 0 },
      { integrationId: 'int_1', projectId: 'prj_1', class: 'status_sync', label: 'Status sync', description: 'Never an ignore.', enabled: false, version: 0 },
    ];
    const s = testServices({
      integrationAutoApprove: async () => classes,
      integrationAutoApproveSet: rec(calls, (req) => {
        classes = classes.map((c) => (c.class === (req as { class: string }).class ? { ...c, enabled: true, version: 1 } : c));
        return classes[1]!;
      }),
    } as Partial<Client>);
    const r = mount(<AutoApprove conn={conn()} projectId="prj_1" />, s);
    await waitFor(() => expect(r.$$('[data-testid="auto-class"][data-enabled="false"]').length).toBe(2), r);
    expect(r.container.textContent).toContain('None covers a delete, an ignore');
    await r.act(() => (byLabel(r.container, 'Status sync') as unknown as HTMLButtonElement).click());
    await waitFor(() => expect(calls[0]).toEqual([{ integrationId: 'int_1', projectId: 'prj_1', class: 'status_sync', enabled: true, version: 0 }]), r);
    await waitFor(() => expect(r.$('[data-class="status_sync"]')!.getAttribute('data-enabled')).toBe('true'), r);
  });

  it('attachments are read, downloaded and shown as text only on the person’s action; a link is never fetched', async () => {
    const calls: Calls = [];
    let atts: IntegrationAttachment[] = [];
    const base = { itemId: 'itm_1', integrationId: 'int_1', mime: 'text/plain', size: 0, textKept: false, redactions: 0, risk: 0, listedAt: 1, untrusted: true };
    const s = testServices({
      integrationAttachments: async () => atts,
      integrationAttachmentsRefresh: rec(calls, () =>
        (atts = [
          { ...base, id: 'att_1', externalId: 'a1', name: 'crash.log', downloadable: true },
          { ...base, id: 'att_2', externalId: 'a2', name: 'PR 12', mime: '', url: 'https://github.com/acme/web/pull/12', downloadable: false },
        ]),
      ),
      integrationAttachmentDownload: rec(calls, () => {
        atts = atts.map((a) => (a.id === 'att_1' ? { ...a, downloadedAt: 5, textKept: true, redactions: 1, size: 40 } : a));
        return atts[0]!;
      }),
      integrationAttachmentText: rec(calls, () => ({ id: 'att_1', itemId: 'itm_1', name: 'crash.log', text: 'key [REDACTED:aws-access-token]', redactions: 1, risk: 0, untrusted: true })),
    } as Partial<Client>);
    const r = mount(<ItemAttachments item={item()} />, s);
    await waitFor(() => expect(r.container.textContent).toContain('No attachments listed yet.'), r);
    expect(calls.length).toBe(0);
    await r.act(() => byText(r.container, 'button', 'Read the list from Linear').click());
    await waitFor(() => expect(r.$$('[data-testid="attachment"]').length).toBe(2), r);
    const link = r.$('[data-testid="attachment"][data-downloadable="false"]') as HTMLElement;
    expect(link.querySelector('button')).toBeNull();
    expect(link.querySelector('a')!.getAttribute('href')).toBe('https://github.com/acme/web/pull/12');
    await r.act(() => byText(r.container, 'button', 'Download').click());
    await waitFor(() => expect(r.$('[data-downloaded="true"]')).not.toBeNull(), r);
    expect(r.container.textContent).toContain('1 secret removed');
    await r.act(() => byText(r.container, 'button', 'Show text').click());
    await waitFor(() => expect(r.$('[data-testid="attachment-text"]')!.textContent).toBe('key [REDACTED:aws-access-token]'), r);
  });

  it("a typed row shows its properties; the service's status of a linked task is offered, and applied only by the person", async () => {
    const calls: Calls = [];
    const it_ = item({
      source: 'notion',
      taskId: 'task_1',
      properties: { Status: { type: 'status', text: 'Done' }, Estimate: { type: 'number', text: '3.5' } },
      statusSuggestion: { upstream: 'Done', to: 'COMPLETED', taskStatus: 'IN_PROGRESS' },
    });
    const changed: number[] = [];
    const s = testServices({ integrationStatusApply: rec(calls, () => it_) } as Partial<Client>);
    const r = mount(
      <div>
        <PropsTable item={it_} />
        <StatusSuggestionNote item={it_} onChanged={() => void changed.push(1)} />
      </div>,
      s,
    );
    const props = await need(r, '[data-testid="item-props"]');
    expect(Array.from(props.querySelectorAll('dt')).map((d) => d.textContent)).toEqual(['Estimate', 'Status']);
    const note = r.$('[data-testid="status-suggestion"]') as HTMLElement;
    expect(note.textContent).toContain('Notion says Done');
    expect(calls.length).toBe(0);
    await r.act(() => byText(note, 'button', 'Move the task to COMPLETED').click());
    await waitFor(() => expect(calls).toEqual([['itm_1']]), r);
    await waitFor(() => expect(changed.length).toBe(1), r);
  });
});
