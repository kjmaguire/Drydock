// A chat from Home, without a project, with a skill from the workspace skill library and an MCP server from the workspace MCP
// library (decision P60, on the owner's request 2026-10-15). Against the real core. The agent CLI is the harness's scripted
// stand-in (cmd/drydock-e2e, a TEST DOUBLE that speaks Claude Code's stream-json): for a message carrying "drydock-e2e props"
// it reads the wrangler skill through the gateway's skills tool and calls the granted server's echo tool, and says what each
// answered. The skill comes from the harness's registry fixture; the server is the harness binary re-executed as the stdio
// test server (DRYDOCK_E2E_MCP_SERVER=1, a TEST FIXTURE).
import { expect, test } from '@playwright/test';
import { appUrl, sidebar } from './helpers';

// the suite shares one harness: the probe server leaves the MCP library again, so a later spec finds the library as it was
test.afterEach(async ({ request }) => {
  const url = new URL(appUrl());
  await request.post(`${url.origin}/rpc/mcplib.delete`, { headers: { 'X-Drydock-Token': url.searchParams.get('token') ?? '' }, data: { id: 'chatmcp' } });
});

test('Home starts a chat without a project; its skill and MCP server reach the session; Properties take the server away', async ({ page }) => {
  const url = appUrl();
  const origin = new URL(url).origin;
  const token = new URL(url).searchParams.get('token') ?? '';
  const rpc = async (method: string, data: unknown) => {
    const r = await page.request.post(`${origin}/rpc/${method}`, { headers: { 'X-Drydock-Token': token }, data });
    expect(r.ok(), `${method}: ${await r.text()}`).toBe(true);
  };
  // the workspace libraries: the registry fixture's skills, and the stdio test server with echo declared read-only
  await rpc('skill.registryCheck', { registryId: 'cloudflare-skills' });
  await rpc('mcplib.save', { id: 'chatmcp', display: 'Home chat probe', transport: 'stdio', argv: [process.env.DRYDOCK_E2E_EXE], env: { DRYDOCK_E2E_MCP_SERVER: '1' }, readOnlyTools: ['echo'] });

  await page.goto(url);
  await sidebar(page).getByTestId('nav-workspace').click();
  await expect(page).toHaveURL(/\/home$/);
  const home = page.getByTestId('home-chat');
  await expect(home).toBeVisible();
  await home.getByLabel('Message to Claude Code').fill('drydock-e2e props: read the wrangler skill, then echo');

  // Options: the new chat dialog without a project, Claude Code, a private folder, one skill and one server
  await home.getByRole('button', { name: 'Options...' }).click();
  const dialog = page.getByRole('dialog', { name: 'New chat' });
  await expect(dialog.getByLabel('Project', { exact: true })).toHaveAttribute('data-value', '');
  await expect(dialog.getByLabel('Agent CLI', { exact: true })).toHaveAttribute('data-value', 'claude');
  await expect(dialog.getByLabel('Folder', { exact: true })).toHaveValue('');
  await dialog.getByLabel('Filter skills').fill('wrangler');
  await dialog.locator('[data-testid=chat-skill-option][data-name=wrangler] input').check();
  await dialog.locator('[data-testid=chat-mcp-option][data-server=chatmcp] input').check();
  await dialog.getByRole('button', { name: 'Start chat' }).click();

  // the chat opens with the message sent; the stand-in read the skill's text and the server answered through the gateway
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const log = page.getByTestId('chat-log');
  await expect(log).toContainText('drydock-e2e props: read the wrangler skill, then echo');
  await expect(log).toContainText("Use the project's Wrangler version; never guess flags.");
  await expect(log).toContainText('echo: hello from home');
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');
  await expect(page.getByTestId('chip-skills')).toHaveText('1 skill');
  await expect(page.getByTestId('chip-mcp')).toHaveText('1 MCP server');
  await expect(page.getByTestId('chat-cwd')).toHaveAttribute('title', /chats/);

  // Properties: the server is taken away; the change is in the history and applies from the next message
  await page.getByRole('button', { name: 'Properties' }).click();
  const props = page.getByRole('dialog', { name: 'Chat properties' });
  await expect(props.locator('[data-testid=chat-skill-option][data-name=wrangler] input')).toBeChecked();
  await props.locator('[data-testid=chat-mcp-option][data-server=chatmcp] input').uncheck();
  await props.getByRole('button', { name: 'Save' }).click();
  await expect(props).toBeHidden();
  await expect(page.getByTestId('chip-mcp')).toHaveCount(0);
  await expect(log).toContainText('No MCP servers for this chat from the next message.');

  const message = page.getByLabel('Message', { exact: true });
  await message.fill('drydock-e2e props: and again');
  await message.press('Control+Enter');
  await expect(message).toHaveValue('');
  // the skill still reaches the session; the server no longer does
  const last = page.getByTestId('chat-message').last();
  await expect(last).toContainText('failed', { timeout: 30_000 });
  // in both turns the skills tool's card holds what it returned (P84) and the stand-in quotes it
  await expect(page.locator('[data-testid=chat-message][data-role=tool]').filter({ hasText: 'never guess flags' })).toHaveCount(2);
  await expect(page.locator('[data-testid=chat-message][data-role=assistant]').filter({ hasText: 'never guess flags' })).toHaveCount(2);
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');

  // back on Home, the chat is among the latest chats without a project
  await sidebar(page).getByTestId('nav-workspace').click();
  await expect(page.getByTestId('home-chat-recent').filter({ hasText: 'drydock-e2e props' }).first()).toBeVisible();
});
