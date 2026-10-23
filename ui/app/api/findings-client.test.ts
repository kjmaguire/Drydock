import { expect } from 'remix/assert';
import { describe, it, mock } from 'remix/test';
import { createClient } from './client.ts';
import { HttpTransport } from './transport.ts';

// The client methods of decision P97 (Security, Chat, Models on this PC): the parameter names the core decodes, and Go's nil
// slices (JSON null) turned into arrays.

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });

describe('decision P97 client methods', () => {
  it('sends the parameter names the core decodes and normalizes nulls', async () => {
    const rep = { id: 's1', projectId: 'p', dir: '/r', status: 'done', by: 'human:k', startedAt: 1, engines: null, findings: [{ engine: 'deps', severity: 'high', rule: 'Go', title: 't', ids: null, unfixed: null }], counts: null };
    const f = mock.fn(async (url: string) => {
      if (url.endsWith('security.finding.dismiss') || url.endsWith('security.finding.restore')) return json({ result: rep });
      if (url.endsWith('security.finding.dismissed')) return json({ result: null });
      if (url.endsWith('security.finding.context') || url.endsWith('security.file.read'))
        return json({ result: { scanId: 's1', finding: rep.findings[0], lines: null, advice: { meaning: 'm', steps: null } } });
      if (url.endsWith('security.finding.task')) return json({ result: { task: { id: 't1' }, finding: rep.findings[0] } });
      if (url.endsWith('chat.answer')) return json({ result: { answered: true, rule: 'Bash(go test ./...)' } });
      if (url.endsWith('chat.update')) return json({ result: { id: 'chat_1', projectId: 'p', title: '', adapter: 'claude', status: 'idle', allowedTools: null, skills: null, mcpServers: null } });
      if (url.endsWith('local.get')) return json({ result: { runtime: { devices: null }, sources: null, usedBy: null, server: { state: 'running', autostarted: true } } });
      return json({ result: null });
    });
    const c = createClient(new HttpTransport({ token: 'tok', fetchImpl: f as unknown as typeof fetch }));

    const dismissed = await c.securityFindingDismiss({ projectId: 'p' }, 'fp1', 'not_real');
    expect(dismissed.engines).toEqual([]);
    expect(dismissed.findings[0]).toMatchObject({ ids: [], unfixed: [] });
    await c.securityFindingRestore({ folder: 'C:\\code\\shop' }, 'fp1');
    expect(await c.securityFindingsDismissed({ projectId: 'p' })).toEqual([]);
    const cx = await c.securityFindingContext('s1', 'fp1');
    expect(cx.lines).toEqual([]);
    expect(cx.advice).toMatchObject({ meaning: 'm', steps: [], refs: [] });
    const file = await c.securityFileRead('s1', 'fp1');
    expect(file.lines).toEqual([]);
    expect(file.finding).toMatchObject({ ids: [], unfixed: [] });
    expect((await c.securityFindingTask('s1', 'fp1', 'agt_1')).task.id).toBe('t1');
    expect(await c.chatAllowAndRemember('chat_1', 'cask_1')).toEqual({ answered: true, rule: 'Bash(go test ./...)' });
    expect(await c.chatUpdate({ id: 'chat_1', projectId: 'p' })).toMatchObject({ projectId: 'p', allowedTools: [] });
    const v = await c.localGet();
    expect(v.usedBy).toEqual([]);
    expect(v.server.autostarted).toBe(true);

    const sent = f.mock.calls.map((x) => x.arguments).map((x) => [(x as unknown as [string])[0].replace('/rpc/', ''), JSON.parse(((x as unknown as [string, RequestInit])[1].body as string) || 'null')]);
    const all = (m: string) => sent.filter((x) => x[0] === m).map((x) => x[1]);
    expect(all('security.finding.dismiss')).toEqual([{ projectId: 'p', fingerprint: 'fp1', reason: 'not_real' }]);
    expect(all('security.finding.restore')).toEqual([{ folder: 'C:\\code\\shop', fingerprint: 'fp1' }]);
    expect(all('security.finding.dismissed')).toEqual([{ projectId: 'p' }]);
    expect(all('security.finding.context')).toEqual([{ scanId: 's1', fingerprint: 'fp1', around: 0 }]);
    expect(all('security.file.read')).toEqual([{ scanId: 's1', fingerprint: 'fp1' }]);
    expect(all('security.finding.task')).toEqual([{ scanId: 's1', fingerprint: 'fp1', assignee: 'agt_1' }]);
    expect(all('chat.answer')).toEqual([{ id: 'chat_1', askId: 'cask_1', allow: true, remember: true }]);
    expect(all('chat.update')).toEqual([{ id: 'chat_1', projectId: 'p' }]);
  });
});
