import { expect, test } from '@playwright/test';
import { appUrl, choose, createAgent, createProject, openAgentPage, openTile, workspaceNav, projectNav, switchProject } from './helpers';

// Round 6: your own MCP servers and the Library, against the real Go core. An MCP server is connected once, for the workspace,
// on the Integrations page (Add MCP server; it is then a row under Your servers, decision P98); a project adds it from its own
// Integrations page and grants it to one of its agents there. A team member is defined once as a template in the Library and
// added to another project from its Crew page. Neither page chooses anything for a project (decision P51). The server is a real
// stdio MCP server (the e2e binary re-executed with DRYDOCK_E2E_MCP_SERVER=1, a test fixture). What an agent does with it is
// covered by the Go tests.

test("your servers: connect an MCP server once; a project adds it and grants it to an agent on its Integrations page; a template joins another project's team; projects stay separate", async ({ page }) => {
  await createProject(page, 'lib-a');
  await createAgent(page, 'Pay', 'backend');
  await createAgent(page, 'Rev', 'reviewer');
  await createProject(page, 'lib-b'); // now the current project
  await workspaceNav(page, 'nav-integrations');
  await expect(page.getByRole('heading', { level: 1, name: 'Integrations' })).toBeVisible();
  const list = page.getByRole('navigation', { name: 'All integrations' });

  // connect once: Add MCP server opens the server form; a refused address says why, then a real server with a secret
  const exe = process.env.DRYDOCK_E2E_EXE!;
  await page.getByRole('button', { name: 'Add MCP server' }).click();
  const form = page.getByTestId('mcp-form');
  await form.getByLabel('Short id', { exact: false }).fill('web');
  await form.getByLabel('Name', { exact: true }).fill('Remote');
  await choose(form.getByLabel('Kind'), 'http');
  await form.getByLabel('Address', { exact: false }).fill('http://example.com/mcp');
  await form.getByRole('button', { name: 'Connect server' }).click();
  await expect(form.getByRole('alert')).toContainText('plain http');
  await choose(form.getByLabel('Kind'), 'stdio');
  await form.getByLabel('Short id', { exact: false }).fill('tst');
  await form.getByLabel('Name', { exact: true }).fill('Test server');
  await form.getByLabel('Command, one argument per line', { exact: false }).fill(exe);
  await form.getByLabel('Environment, NAME=value per line', { exact: false }).fill('DRYDOCK_E2E_MCP_SERVER=1');
  await form.getByRole('button', { name: 'Add a secret' }).click();
  // a pasted NAME=value in the name of a secret is refused on the page (L38): nothing is saved, and the message does not repeat it
  const saved: string[] = [];
  page.on('request', (req) => {
    if (req.url().includes('/rpc/mcplib.save')) saved.push(req.postData() ?? '');
  });
  await form.getByLabel('Secret name 1').fill('API_KEY=sekret-e2e-value-123456');
  await form.getByLabel('Secret value 1').fill('another-e2e-value');
  await form.getByRole('button', { name: 'Connect server' }).click();
  await expect(form.getByRole('alert')).toHaveText(/^Secret name 1 is not a variable name\./);
  await expect(form.getByLabel('Secret name 1')).toHaveAttribute('aria-invalid', 'true');
  await expect(form.getByLabel('Secret name 1')).toBeFocused();
  await expect(page.locator('body')).not.toContainText('sekret-e2e-value-123456');
  expect(saved).toEqual([]);
  await form.getByLabel('Secret name 1').fill('API_KEY');
  await form.getByLabel('Secret value 1').fill('sekret-e2e-value-123456');
  await form.getByRole('button', { name: 'Connect server' }).click();

  // the new server is a row under Your servers, chosen, with its detail beside the list and in the address
  const serverRow = list.getByTestId('list-group-servers').getByTestId('integration-row').filter({ hasText: 'Test server' });
  await expect(serverRow).toHaveAttribute('aria-current', 'true');
  await expect(serverRow).toContainText('No tools yet');
  await expect(page).toHaveURL(/\/integrations\/server-tst$/);
  await expect(page.getByTestId('integrations-note')).toContainText('Test server is connected. Press Find tools');
  const card = page.getByTestId('server-detail').filter({ hasText: 'Test server' });
  await expect(card).toBeVisible();
  await expect(card.getByTestId('mcp-secrets')).toContainText('API_KEY');
  await expect(page.locator('body')).not.toContainText('sekret-e2e-value-123456');
  await expect(card.getByTestId('server-projects')).toContainText('Not in any project yet');

  // find its tools by really starting it; the human decides which tool only reads
  await card.getByRole('button', { name: 'Find tools' }).click();
  await expect(card.getByTestId('mcp-tool')).toHaveCount(4);
  // the fixture's poisoned tool is held back, the others are not
  await expect(card.getByTestId('mcp-tool-flagged')).toHaveCount(1);
  await expect(card.locator('[data-testid="mcp-tool"][data-tool="format"]').getByTestId('mcp-tool-flagged')).toHaveText('held back');
  await card.getByLabel('echo only reads').click();
  await expect(card.getByLabel('echo only reads')).toBeChecked();
  await expect(serverRow).toContainText('On');
  // a workspace page: the server's detail names no project
  await expect(card).not.toContainText('lib-a');

  // a team member: defined once in the Library
  await workspaceNav(page, 'nav-library');
  await expect(page.getByRole('heading', { level: 1, name: 'Library' })).toBeVisible();
  await page.getByRole('button', { name: 'New agent template' }).click();
  const tf = page.getByTestId('template-form');
  await tf.getByLabel('Template name').fill('Payments dev');
  await tf.getByLabel('Role').fill('backend');
  await tf.getByRole('textbox', { name: /^Purpose/ }).fill('Own the payments service.');
  await choose(tf.getByLabel('Model', { exact: true }), 'sonnet');
  await choose(tf.getByLabel('Effort', { exact: true }), 'high');
  await tf.getByLabel('Template gets Test server').check();
  await tf.getByRole('button', { name: 'Create template' }).click();
  const tpl = page.getByTestId('lib-template').filter({ hasText: 'Payments dev' });
  await expect(tpl).toBeVisible();
  const tplTile = page.getByTestId('lib-template-tile').filter({ hasText: 'Payments dev' });
  await expect(tplTile).toContainText('Gets 1 server');

  // project lib-a adds the server from its own Integrations page, and only Pay may use it
  await switchProject(page, 'lib-a');
  await projectNav(page, 'nav-project-integrations');
  await expect(page.getByTestId('project-integrations')).toBeVisible();
  // the connected servers are compact rows, like the workspace's Integrations page: Test server is not in the project yet
  const tstTile = page.locator('[data-testid="project-server-tile"][data-server="tst"]');
  await expect(tstTile).toHaveAttribute('data-added', 'false');
  await openTile(tstTile);
  const lib = page.locator('[data-testid="library-server"][data-server="tst"]');
  await lib.getByRole('button', { name: 'Add Test server to this project' }).click();
  const onA = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(onA).toBeVisible();
  await expect(lib).toHaveCount(0);
  await expect(tstTile).toHaveAttribute('data-added', 'true');
  await onA.getByLabel('Pay may use Test server').check();
  await expect(onA.getByLabel('Pay may use Test server')).toBeChecked();
  await expect(onA.getByLabel('Rev may use Test server')).not.toBeChecked();
  await expect(onA.getByText('reads only')).toHaveCount(1);
  await expect(tstTile).toContainText('Pay may use it');

  // the template joins lib-b's team from its Crew page (Templates), with the server it names
  await switchProject(page, 'lib-b');
  await projectNav(page, 'nav-team');
  await page.getByRole('button', { name: 'Templates' }).click();
  await expect(page.getByLabel('Template', { exact: true })).toContainText('Payments dev'); // the only template, chosen
  await page.getByRole('button', { name: 'Add to this team' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Payments dev' })).toBeVisible();
  await page.keyboard.press('Escape');
  await openAgentPage(page, 'Payments dev');
  await expect(page.getByTestId('agent-chip').filter({ hasText: 'effort: high' })).toBeVisible();

  // lib-b's Integrations page agrees: granting put the server into lib-b, for Payments dev only; lib-a's grants are not here
  await projectNav(page, 'nav-project-integrations');
  await openTile(page.locator('[data-testid="project-server-tile"][data-server="tst"]'));
  const onB = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(onB.getByLabel('Payments dev may use Test server')).toBeChecked();
  await expect(onB.getByLabel('Pay may use Test server')).toHaveCount(0);

  // the workspace's Integrations page counts the projects without naming them
  await workspaceNav(page, 'nav-integrations');
  await serverRow.click();
  const again = page.getByTestId('server-detail').filter({ hasText: 'Test server' });
  await expect(again.getByTestId('server-projects')).toContainText('In 2 projects.');
  await expect(again).not.toContainText('lib-a');

  // removing it takes it out of every project, after a confirmation that says so
  await again.getByRole('button', { name: 'Remove' }).click();
  await expect(again.getByTestId('server-remove-confirm')).toContainText('It leaves 2 projects');
  await again.getByRole('button', { name: 'Yes, remove' }).click();
  await expect(page.getByTestId('integrations-note')).toContainText('Test server was removed.');
  await expect(serverRow).toHaveCount(0);
  await expect(page.getByTestId('server-detail')).toHaveCount(0);
  await projectNav(page, 'nav-project-integrations', 'lib-a');
  await expect(page.getByTestId('no-project-servers')).toBeVisible();
  await expect(page.getByTestId('mcp-server')).toHaveCount(0);
});

// Audit 2026-10-16, M24: a configuration file can come from a repository and name any program. Importing it only saves: nothing is
// started until the person presses Find tools in the server's own detail, and a server that runs a program is not ticked for them.
// The program here is the e2e binary re-executed as a real stdio MCP server, so "started" is visible: its tools are found.
test('your servers: importing servers from a file starts nothing; a program is not ticked for you; Find tools names the program and is the first thing that runs it (M24)', async ({ page }) => {
  const exe = process.env.DRYDOCK_E2E_EXE!;
  await page.goto(appUrl());
  await workspaceNav(page, 'nav-integrations');
  await expect(page.getByRole('heading', { level: 1, name: 'Integrations' })).toBeVisible();
  const servers = page.getByRole('navigation', { name: 'All integrations' }).getByTestId('list-group-servers');
  // what the page asks of the core about servers, in order
  const asked: string[] = [];
  page.on('request', (req) => {
    const m = /\/rpc\/(mcplib\.[A-Za-z]+)/.exec(req.url());
    if (m) asked.push(m[1]!);
  });
  const program = { command: exe, env: { DRYDOCK_E2E_MCP_SERVER: '1' } };
  const both = JSON.stringify({ mcpServers: { importprog: program, importweb: { url: 'https://example.com/mcp' } } });
  const openImport = async (config: string) => {
    await page.getByRole('button', { name: 'Add MCP server' }).click();
    await page.getByTestId('mcp-add-custom').getByRole('button', { name: 'Import from a file' }).click();
    const panel = page.getByTestId('mcp-import');
    await panel.getByLabel('Configuration (JSON)').fill(config);
    await panel.getByRole('button', { name: 'Read servers' }).click();
    return panel;
  };

  // the program is badged, shown whole and not ticked; the address is ticked and shown
  let panel = await openImport(both);
  const prog = panel.locator('[data-testid="import-candidate"][data-id="importpr"]');
  await expect(prog).toHaveAttribute('data-kind', 'program');
  await expect(prog.getByTestId('import-runs-program')).toHaveText('Runs a program on this computer');
  await expect(prog.getByTestId('import-command')).toContainText(exe);
  await expect(prog.getByRole('checkbox')).not.toBeChecked();
  const web = panel.locator('[data-testid="import-candidate"][data-id="importwe"]');
  await expect(web.getByRole('checkbox')).toBeChecked();
  await expect(web.getByTestId('import-address')).toHaveText('https://example.com/mcp');
  await expect(panel.getByRole('button', { name: 'Import 1 server' })).toBeEnabled();

  // importing the ticked address saves it and starts nothing; the page says so and where to start one
  await panel.getByRole('button', { name: 'Import 1 server' }).click();
  await expect(page.getByTestId('integrations-note')).toContainText('1 server imported. Nothing has been started');
  await expect(servers.getByTestId('integration-row').filter({ hasText: 'importweb' })).toBeVisible();
  await expect(servers.getByTestId('integration-row').filter({ hasText: 'importprog' })).toHaveCount(0);

  // the person ticks the program and imports it: saved, and still not started
  panel = await openImport(JSON.stringify({ mcpServers: { importprog: program } }));
  const prog2 = panel.locator('[data-testid="import-candidate"][data-id="importpr"]');
  await prog2.getByRole('checkbox').check();
  await panel.getByRole('button', { name: 'Import 1 server' }).click();
  const row = servers.getByTestId('integration-row').filter({ hasText: 'importprog' });
  await expect(row).toBeVisible();
  await expect(row).toContainText('No tools yet');
  expect(asked.filter((x) => x === 'mcplib.save')).toHaveLength(2);
  expect(asked).not.toContain('mcplib.discover');

  // Find tools is in the server's own detail, says which program it starts, and is the first thing that runs it
  await row.click();
  const card = page.getByTestId('server-detail').filter({ hasText: 'importprog' });
  await expect(card.getByTestId('reach-note')).toContainText('Pressing Find tools starts this program on this computer');
  await expect(card.getByTestId('reach-note').getByTestId('command-line')).toContainText(exe);
  expect(asked).not.toContain('mcplib.discover');
  await card.getByRole('button', { name: 'Find tools' }).click();
  await expect(card.getByTestId('mcp-tool')).toHaveCount(4);
  expect(asked).toContain('mcplib.discover');

  // leave the servers as they were found: later specs expect to see only their own servers
  for (const name of ['importprog', 'importweb']) {
    await servers.getByTestId('integration-row').filter({ hasText: name }).click();
    const c = page.getByTestId('server-detail').filter({ hasText: name });
    await c.getByRole('button', { name: 'Remove' }).click();
    await c.getByRole('button', { name: 'Yes, remove' }).click();
    await expect(servers.getByTestId('integration-row').filter({ hasText: name })).toHaveCount(0);
  }
});
