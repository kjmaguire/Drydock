import { expect, test } from '@playwright/test';
import { choose, createAgent, createAndStartTask, createAssignedTask, createProject, docsDir, freshGreenRepo, roomLink, workspaceNav, projectNav, openRoom, openAgentPage } from './helpers';

// Slice 2-5 screens against the real Go core. The agent process is the scripted fake agent; this integrations test has no
// vendor server, so only the refusal paths are exercised (integrations.spec.ts runs against a protocol fixture).

test('economics: a budget is set and listed, the evals report renders, routing rules and a preview are shown, telemetry is off and local', async ({ page }) => {
  await createProject(page, 'gocalc-econ');

  // budgets
  await projectNav(page, 'nav-budgets');
  await expect(page.getByText('No budgets')).toBeVisible();
  await page.getByRole('button', { name: 'Add budget' }).first().click();
  // a limit that is not a plain number is refused under its field and never saved as "no limit" (M25): the core is not asked
  const budgetSets: string[] = [];
  page.on('request', (req) => {
    if (req.url().includes('/rpc/budget.set')) budgetSets.push(req.postData() ?? '');
  });
  await page.getByLabel('Limit in USD').fill('5 dollars');
  await page.getByRole('button', { name: 'Add budget' }).last().click();
  await expect(page.getByLabel('Limit in USD')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByRole('alert')).toHaveText('Enter the limit in dollars, for example 12.50.');
  await expect(page.getByLabel('Limit in USD')).toBeFocused();
  await expect(page.getByTestId('budget')).toHaveCount(0);
  expect(budgetSets).toEqual([]);
  await page.getByLabel('Limit in USD').fill('5');
  await page.getByLabel('Note').fill('e2e budget');
  await page.getByRole('button', { name: 'Add budget' }).last().click();
  const card = page.getByTestId('budget').first();
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute('data-state', 'ok');
  await expect(card).toContainText('$5');
  await expect(card).toContainText('warns at 80%');
  await expect(card).toContainText('e2e budget');
  await card.getByRole('button', { name: 'Edit limits' }).click();
  await page.getByLabel('Limit in USD').fill('7.5');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByTestId('budget').first()).toContainText('$7.5');
  // a budget with no limit is refused by the core and its words are shown
  await page.getByRole('button', { name: 'Add budget' }).first().click();
  await page.getByLabel('Scope id (an id, or * for every task, run or agent)').fill('*');
  await choose(page.getByLabel('Scope', { exact: true }), 'task');
  await page.getByRole('button', { name: 'Add budget' }).last().click();
  await expect(page.getByRole('alert')).toContainText('set at least one of limitUsd, limitTokens, limitTurns');
  await page.reload();
  await expect(page.getByTestId('budget')).toHaveCount(1);

  // evals
  await projectNav(page, 'nav-evals');
  await expect(page.getByTestId('evals-markdown')).toBeVisible();
  await expect(page.getByTestId('evals-headline')).toBeVisible();
  await expect(page.getByTestId('ratio-completion')).toHaveText('n/a'); // no task has started: a zero denominator is n/a
  await page.getByRole('button', { name: 'Export to a local file' }).click();
  await expect(page.getByTestId('evals-exported')).toContainText('stays on this machine');

  // routing
  await workspaceNav(page, 'nav-routing');
  await expect(page.getByTestId('route-rule').first()).toBeVisible();
  await page.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(page.getByTestId('route-preview')).toContainText('Recorded: no');
  // every agent turn kind can be previewed, with the signals a real turn of that kind gives the router (P63)
  await choose(page.getByLabel('Run kind'), 'room_reply');
  await page.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(page.getByTestId('route-preview')).toContainText('Recorded: no');
  // the tiebreak is off and no turn has a wall-time limit until the person sets one (P112); a limit set is read back
  const limits = page.getByTestId('route-limits');
  await expect(limits.getByLabel(/Ask the cheap model when the rules leave a tie/)).not.toBeChecked();
  await expect(limits.getByTestId('limits-now')).toHaveText('No limit is set.');
  await limits.getByLabel('Balanced (minutes)').fill('45');
  await limits.getByRole('button', { name: 'Save limits' }).click();
  await expect(limits.getByTestId('limits-now')).toHaveText('Set: Balanced 45 min.');
  await page.reload();
  await expect(page.getByTestId('route-limits').getByLabel('Balanced (minutes)')).toHaveValue('45');

  // telemetry: off by default, local only, a preview of what an export holds
  await workspaceNav(page, 'nav-settings');
  const tel = page.getByTestId('telemetry');
  await expect(tel).toContainText('off');
  await expect(tel).toContainText('this device only');
  await tel.getByRole('button', { name: 'Preview what an export contains' }).click();
  await expect(page.getByTestId('telemetry-preview')).toContainText('allowlisted');
  await expect(tel.getByTestId('telemetry-status')).toHaveAttribute('data-tone', 'muted');
  await expect(tel).toContainText('Off unless you turn it on.');
  await tel.getByRole('button', { name: 'Turn telemetry on' }).click();
  await expect(tel.getByRole('button', { name: 'Turn telemetry off' })).toBeVisible();
  await expect(tel.getByTestId('telemetry-status')).toHaveText('on');
  await expect(tel.getByTestId('telemetry-status')).toHaveAttribute('data-tone', 'ok');
  await tel.getByRole('button', { name: 'Turn telemetry off' }).click();
  await expect(tel.getByRole('button', { name: 'Turn telemetry on' })).toBeVisible();
  // sending to a collector (decision P113): off with no endpoint; plain http to another computer is refused by the core
  const otlp = tel.getByTestId('otlp');
  await expect(otlp.getByTestId('otlp-status')).toContainText('Not sending: sending is off.');
  await expect(otlp.getByRole('button', { name: 'Send a test span' })).toBeDisabled();
  await otlp.getByLabel('Endpoint (OTLP/HTTP)').fill('http://192.0.2.7:4318');
  await otlp.getByRole('button', { name: 'Save endpoint' }).click();
  await expect(otlp).toContainText('plain http is allowed only to this computer');
  await otlp.getByLabel('Endpoint (OTLP/HTTP)').fill('http://127.0.0.1:4318');
  await otlp.getByRole('button', { name: 'Save endpoint' }).click();
  await expect(otlp.getByRole('button', { name: 'Send a test span' })).toBeEnabled();
  await expect(otlp.getByTestId('otlp-status')).toContainText('Not sending: sending is off.');

  // project settings: checks and the development flow
  await projectNav(page, 'nav-project');
  await page.getByLabel('Dependency vulnerability scan').check();
  await page.getByRole('button', { name: 'Save check settings' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Saved.' }).first()).toBeVisible();
  await page.getByLabel('Maximum parallel agent turns').fill('2');
  await page.getByLabel('Agents answer in channels and direct messages').check();
  const workerChecks = page.getByLabel(/Let Claude workers run this project's own checks/);
  await expect(workerChecks).not.toBeChecked(); // off until the human says so
  await workerChecks.check();
  await expect(page.getByTestId('worker-checks-note')).toContainText('any program with your user');
  // CI before merge: required until the human declares that the repository has no CI; the core records who and when
  await expect(page.getByLabel('Required (default)', { exact: true })).toBeChecked();
  await page.getByLabel('This repository has no CI: merge after the review alone', { exact: true }).check();
  await expect(page.getByTestId('ci-none-note')).toContainText('recorded when you save');
  await page.getByRole('button', { name: 'Save development settings' }).click();
  await expect(page.getByTestId('ci-none-note')).toContainText(/^Declared by human:\S+ on /);
  await page.reload();
  await expect(page.getByLabel('Maximum parallel agent turns')).toHaveValue('2');
  await expect(page.getByLabel(/Let Claude workers run this project's own checks/)).toBeChecked();
  await expect(page.getByLabel('Dependency vulnerability scan')).toBeChecked();
  await expect(page.getByLabel('This repository has no CI: merge after the review alone', { exact: true })).toBeChecked();
  await expect(page.getByTestId('ci-none-note')).toContainText(/^Declared by human:\S+ on /);
});

test('knowledge: an expert pack from a local folder is searched with citations; a requirement is uploaded and traced; milestones; skills are offered then accepted; memory', async ({ page }) => {
  await createProject(page, 'gocalc-know', freshGreenRepo());
  await createAgent(page, 'Dana', 'backend');

  // expert pack from a local directory
  await projectNav(page, 'nav-packs');
  const dir = docsDir().replace(/\\/g, '/');
  await page.getByLabel('Pack manifest (YAML)').fill(`schema: 1\nid: e2edocs\ntitle: E2E docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: "${dir}"}\nattach: {instructions: "Go concurrency notes."}\n`);
  await page.getByRole('button', { name: 'Install pack' }).click();
  const pack = page.locator('[data-testid=pack][data-pack=e2edocs]');
  await expect(pack).toBeVisible();
  await pack.getByRole('button', { name: 'Refresh now' }).click();
  await expect(pack.getByRole('status')).toContainText('pages');
  await pack.getByLabel('Role for e2edocs').fill('backend');
  await pack.getByRole('button', { name: 'Attach to this project' }).click();
  await expect(pack).toContainText(/backend/i);
  await page.getByLabel('Search packs').fill('goroutine channel');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  const hit = page.getByTestId('pack-hit').first();
  await expect(hit).toBeVisible();
  await expect(hit.getByTestId('citation')).toContainText('e2edocs@');
  await expect(hit).toContainText('goroutine');

  // requirements: upload a file, trace, link to a task
  await projectNav(page, 'nav-requirements');
  // each screen here has a field labelled Title: wait for the new screen before typing, or the text goes into the old one
  await expect(page.getByRole('heading', { name: 'Add requirements' })).toBeVisible();
  await page.getByLabel('Title', { exact: true }).fill('Checkout');
  await page.locator('input[type=file]').setInputFiles({ name: 'checkout.md', mimeType: 'text/markdown', buffer: Buffer.from('# Checkout\n\n- Given a paid cart, when the order is placed, then an order record exists\n\n<script>window.__xss=1</script>\n') });
  await expect(page.locator('textarea').first()).toHaveValue(/paid cart/);
  await page.getByRole('button', { name: 'Add requirement' }).click();
  const detail = page.getByTestId('requirement-detail');
  await expect(detail).toBeVisible();
  await expect(detail.getByTestId('trace')).toHaveAttribute('data-verified', 'false');
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__xss)).toBeUndefined();
  await expect(detail.locator('script')).toHaveCount(0);
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Build checkout');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Build checkout/ })).toBeVisible();
  await projectNav(page, 'nav-requirements');
  await page.getByTestId('requirement').filter({ hasText: 'Checkout' }).getByRole('button', { name: 'Open' }).click();
  await choose(page.getByLabel('Task to link'), { label: '#1 Build checkout' });
  await page.getByLabel(/^Criteria \(one per line/).fill('Given a paid cart, when the order is placed, then an order record exists');
  await page.getByRole('button', { name: 'Link' }).click();
  await expect(page.getByTestId('req-link')).toContainText('Given a paid cart');
  await expect(page.getByTestId('trace-task')).toContainText('Build checkout');
  await expect(page.getByTestId('trace-task')).toContainText('no test evidence');

  // milestones
  await projectNav(page, 'nav-milestones');
  await expect(page.getByRole('heading', { name: 'New milestone' })).toBeVisible();
  await page.getByLabel('Title', { exact: true }).fill('Beta');
  await page.getByRole('button', { name: 'Create milestone' }).click();
  const ms = page.locator('[data-testid=milestone][data-title=Beta]');
  await expect(ms).toBeVisible();
  await choose(ms.getByLabel('Add a task to Beta'), { label: '#1 Build checkout' });
  await ms.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(ms).toContainText('0 of 1 completed');

  // skills: repository skills are offered, never applied; the human opens the skill's row and accepts in the panel that opens
  // inside it (decisions P48, P49)
  await projectNav(page, 'nav-skills');
  await page.getByRole('button', { name: 'Scan the repository for skills' }).click();
  await expect(page.getByTestId('skill-scan')).toContainText('1 offered, 0 accepted');
  const goTile = page.locator('[data-testid=skill-tile][data-name=go-style]');
  await expect(goTile).toHaveAttribute('data-status', 'offered');
  await expect(goTile).toContainText('Offered');
  await goTile.locator('button.what').click();
  const skill = goTile.locator('[data-testid=skill-tile-detail] [data-testid=skill][data-skill=go-style]');
  await expect(skill).toHaveAttribute('data-status', 'offered');
  await skill.getByRole('button', { name: 'Accept go-style' }).click();
  await expect(skill).toHaveAttribute('data-status', 'accepted');
  await expect(goTile).toHaveAttribute('data-check', 'true');
  await expect(goTile).toContainText('Accepted ✓');

  // the Library holds the registries' skills: Sync now fills it from the harness's registry fixture (cloudflare/skills carries
  // three stand-in skills); Add to project makes the skill accepted at once, with a status line and a check on its row
  const cf = page.locator('[data-testid=registry-tile][data-registry=cloudflare-skills]');
  await cf.click();
  await page.locator('[data-testid=registry][data-registry=cloudflare-skills]').getByRole('button', { name: 'Sync now' }).click();
  await expect(cf).toContainText('3 skills');
  const wranglerInLibrary = page.locator('[data-testid=library-skill-tile][data-name=wrangler]');
  await expect(wranglerInLibrary).toBeVisible();
  await wranglerInLibrary.locator('button.what').click();
  // a skill is given to agents, never the whole team (P76): Add waits until the agents it is for are ticked
  const add = wranglerInLibrary.getByTestId('library-skill-tile-detail').getByRole('button', { name: 'Add to gocalc-know' });
  await expect(add).toBeDisabled();
  await page.getByTestId('library-for-agents').getByLabel('Dana (backend)').check();
  await add.click();
  await expect(page.getByTestId('skill-added')).toContainText('wrangler was added to gocalc-know and is accepted ✓');
  await expect(page.getByTestId('skill-added')).toContainText('For Dana');
  const wrangler = page.locator('[data-testid=skill-tile][data-name=wrangler]');
  await expect(wrangler).toHaveAttribute('data-status', 'accepted');
  await expect(wrangler).toHaveAttribute('data-check', 'true');
  await expect(wrangler).toContainText('Accepted ✓');
  await expect(wranglerInLibrary).toHaveCount(0); // it left the "Add from the Library" grid
  await expect(page.locator('[data-testid=skill][data-skill=wrangler]').getByTestId('skill-status')).toHaveText('accepted ✓');

  // memory: propose, accept, delete
  await projectNav(page, 'nav-memory');
  await expect(page.getByRole('heading', { name: 'Propose an entry' })).toBeVisible();
  await page.getByLabel('Title', { exact: true }).fill('Use gofmt');
  await page.getByLabel('Body').fill('Run gofmt before every commit.');
  await page.getByRole('button', { name: 'Propose' }).click();
  const mem = page.locator('[data-testid=memory][data-title="Use gofmt"]');
  await expect(mem).toHaveAttribute('data-status', 'proposed');
  await mem.getByRole('button', { name: 'Accept', exact: true }).click();
  await expect(mem).toHaveAttribute('data-status', 'accepted');
  await mem.getByRole('button', { name: 'Delete' }).click();
  await mem.getByRole('button', { name: 'Yes, delete' }).click();
  await expect(mem).toHaveCount(0);

  // a long proposal is shown whole before it can be accepted (M26): the last line is in the box, with the length beside it
  const long = `${'Run gofmt before every commit.\n'.repeat(240)}LAST-LINE-MARKER`;
  await page.getByLabel('Title', { exact: true }).fill('A long rule');
  await page.getByLabel('Body').fill(long);
  await page.getByRole('button', { name: 'Propose' }).click();
  const big = page.locator('[data-testid=memory][data-title="A long rule"]');
  await expect(big).toHaveAttribute('data-status', 'proposed');
  await expect(big.getByTestId('memory-body')).toContainText('LAST-LINE-MARKER');
  await expect(big.getByTestId('memory-body')).toHaveAttribute('tabindex', '0');
  await expect(big.getByTestId('memory-chars')).toHaveText(`${[...long].length.toLocaleString('en-US')} characters`);
  await big.getByRole('button', { name: 'Delete' }).click();
  await big.getByRole('button', { name: 'Yes, delete' }).click();
  await expect(big).toHaveCount(0);
});

test('development flow: the Feature template stops at the spec gate; reject with feedback, accept with edited criteria, red then green, approve', async ({ page }) => {
  await createProject(page, 'gocalc-feature', freshGreenRepo());
  await createAgent(page, 'Priya', 'pm');
  await createAgent(page, 'Quinn', 'qa');
  await createAgent(page, 'Dana', 'backend');
  await createAgent(page, 'Rex', 'reviewer');
  await createAssignedTask(page, 'Add Multiply', 'Dana', 'Rex');
  await choose(page.getByLabel('Template', { exact: true }), { label: 'Feature' });
  await expect(page.getByTestId('template-info')).toContainText('accept the spec before anything is built');
  await page.getByRole('button', { name: 'Start', exact: true }).click();

  // the spec gate
  const gate = page.getByTestId('spec-gate');
  await expect(gate).toBeVisible({ timeout: 90_000 });
  await expect(gate.getByTestId('spec-criteria').locator('li')).toHaveCount(2);
  await expect(page.getByTestId('task-status').first()).toContainText('Waiting for you');
  // nothing has been built: no red or green evidence yet
  await expect(page.locator('[data-testid=evidence][data-kind=red_test]')).toHaveCount(0);
  // it is in the Inbox as the project's spec gate, with the task's criteria beside it
  await projectNav(page, 'nav-needs-you');
  const specItem = page.getByTestId('needs-item').filter({ hasText: 'gocalc-feature' }).filter({ hasText: 'spec gate' });
  await expect(specItem).toContainText('Add Multiply: the spec is ready');
  await specItem.getByRole('button').click();
  const pane = page.getByRole('complementary', { name: 'Selected item' });
  await expect(pane.getByTestId('detail-kind')).toHaveText('Spec gate · gocalc-feature');
  await expect(pane.getByTestId('detail-criteria').locator('li')).toHaveCount(2);
  await pane.getByTestId('detail-open').click();

  // reject with feedback: a NEW request (new id) comes back after the PM revises
  const first = await page.getByTestId('pending-request').getAttribute('data-request-id');
  await page.getByRole('button', { name: 'Reject with feedback' }).click();
  await page.getByLabel('Feedback for the PM agent').fill('add a criterion about overflow');
  await page.getByRole('button', { name: 'Send spec back' }).click();
  await expect(async () => {
    const id = await page.getByTestId('pending-request').getAttribute('data-request-id', { timeout: 1000 });
    expect(id).toBeTruthy();
    expect(id).not.toBe(first);
  }).toPass({ timeout: 90_000 });

  // accept with the criteria edited (still Given / When / Then)
  await page.getByLabel('Edit criteria').fill('Given 3 and 4, when Multiply is called, then it returns 12\nGiven 0 and 9, when Multiply is called, then the result is 0');
  await page.getByRole('button', { name: 'Accept spec with my edited criteria' }).click();

  // red then green, then the final approval
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 120_000 });
  await expect(page.locator('[data-testid=evidence][data-kind=spec]').first()).toBeVisible();
  await expect(page.locator('[data-testid=evidence][data-kind=spec_accept]').first()).toContainText('Spec accepted');
  await expect(page.locator('[data-testid=evidence][data-kind=red_test]').first()).toContainText('Test written first (red)');
  await expect(page.locator('[data-testid=evidence][data-kind=green_test]').first()).toContainText('Tests now pass (green)');
  await expect(page.locator('main')).toContainText('then the result is 0');
  await page.getByTestId('review-panel').getByRole('button', { name: 'Approve' }).click();
  await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });
  // every agent turn has a recorded routing decision, and the explanation says it was static
  await page.getByRole('button', { name: 'Explain why this model' }).first().click();
  await expect(page.getByTestId('explain')).toContainText('static');
});

test('PlanTeam: the planner proposes a task graph, a dependency-closed subset is accepted and the tasks appear', async ({ page }) => {
  await createProject(page, 'gocalc-plan', freshGreenRepo());
  await createAgent(page, 'Priya', 'pm');
  await projectNav(page, 'nav-plan');
  await page.getByLabel('Goal').fill('Add arithmetic helpers');
  await page.getByRole('button', { name: 'Propose a plan' }).click();
  const plan = page.getByTestId('plan');
  await expect(plan).toBeVisible({ timeout: 90_000 });
  await expect(plan.getByTestId('plan-task')).toHaveCount(2);
  // Divide depends on Multiply: clearing Multiply clears Divide too
  await plan.getByLabel('Create Add Multiply').uncheck();
  await expect(plan.getByLabel('Create Add Divide')).not.toBeChecked();
  await plan.getByLabel('Create Add Divide').check(); // brings Multiply back
  await expect(plan.getByLabel('Create Add Multiply')).toBeChecked();
  await plan.getByLabel('Create Add Divide').uncheck();
  await plan.getByRole('button', { name: 'Accept 1 task' }).click();
  await expect(page.getByTestId('plan')).toHaveAttribute('data-status', 'ACCEPTED', { timeout: 60_000 });
  await projectNav(page, 'nav-tasks');
  await expect(page.getByTestId('task-row').filter({ hasText: 'Add Multiply' })).toHaveCount(1);
  await expect(page.getByTestId('task-row').filter({ hasText: 'Add Divide' })).toHaveCount(0);
});

test('a budget pause stops the next agent turn, appears in Needs you, and only the human resumes it', async ({ page }) => {
  await createProject(page, 'gocalc-budget');
  await createAgent(page, 'Worker B', 'backend');
  await createAgent(page, 'Reviewer B', 'reviewer');
  await projectNav(page, 'nav-budgets');
  await page.getByRole('button', { name: 'Add budget' }).first().click();
  await page.getByLabel('Limit in turns').fill('1');
  await page.getByRole('button', { name: 'Add budget' }).last().click();
  await expect(page.getByTestId('budget').first()).toBeVisible();
  await createAndStartTask(page, 'Budgeted task', 'Worker B', 'Reviewer B');

  // after the first turn the review turn does not start
  const pause = page.getByTestId('budget-pause');
  await expect(pause).toBeVisible({ timeout: 90_000 });
  await expect(pause).toContainText('Budget limit reached');
  await expect(page.getByTestId('pending-request').first()).toHaveAttribute('data-kind', 'approval');
  await projectNav(page, 'nav-needs-you');
  const budgetItem = page.getByTestId('needs-item').filter({ hasText: 'gocalc-budget' }).filter({ hasText: 'Budget limit' });
  // a budget item of its own (P118), not a plain approval
  await expect(budgetItem).toHaveAttribute('data-kind', 'budget');
  await budgetItem.getByRole('button').click();
  // the Inbox does not answer a budget pause: it says where, and opens the task
  const pane = page.getByRole('complementary', { name: 'Selected item' });
  await expect(pane.getByTestId('needs-hint')).toContainText('budget stopped the next agent turn');
  await expect(pane.getByTestId('detail-approve')).toHaveCount(0);
  await pane.getByTestId('detail-open').click();

  // raise the limit and resume: the run continues to the review panel
  await page.getByLabel('New turn limit').fill('10');
  await page.getByRole('button', { name: 'Resume' }).click();
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByTestId('budget-pause')).toHaveCount(0);
  await projectNav(page, 'nav-budgets');
  await expect(page.getByTestId('budget').first()).toContainText('10 turns');
});

test('every turn is routed: a routed agent answers in the room on the router\'s choice, and the room says why', async ({ page }) => {
  await createProject(page, 'gocalc-routed-reply');
  await createAgent(page, 'Rita', 'backend');
  // the human lists the models the router may choose between for Rita
  await openAgentPage(page, 'Rita');
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const editor = page.getByTestId('agent-editor');
  await editor.getByText('More settings').click();
  await editor.getByLabel("Let the router choose this agent's model for each turn").check();
  await choose(editor.getByLabel('Routed model 1', { exact: true }), 'haiku');
  await choose(editor.getByLabel('Tier of routed model 1', { exact: true }), 'cheap');
  await choose(editor.getByLabel('Routed model 2', { exact: true }), 'opus');
  await choose(editor.getByLabel('Tier of routed model 2', { exact: true }), 'premium');
  await editor.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByTestId('agent-editor')).toHaveCount(0);
  await projectNav(page, 'nav-project');
  await page.getByLabel('Agents answer in channels and direct messages').check();
  await page.getByRole('button', { name: 'Save development settings' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Saved.' }).first()).toBeVisible();

  await openRoom(page, 'general');
  const box = page.getByLabel('Message to #general');
  await box.fill('@rita drydock-e2e routed reply: what does Subtract do?');
  await box.press('Control+Enter');
  await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'Answered by the scripted room stand-in' })).toBeVisible({ timeout: 90_000 });
  // the room's agents and turns are the Room tab on the right; the mode chip in the header opens it (UI review P1-5)
  await page.getByTestId('room-mode').click();
  const panel = page.getByTestId('room-agents');
  await panel.getByTestId('room-turns').locator('summary').click();
  const model = panel.getByTestId('room-turn-model').first();
  await expect(model).toContainText('haiku'); // a reply is light: the router's cheap choice among Rita's own models
  await model.getByRole('button', { name: 'Why this model?' }).click();
  await expect(panel.getByTestId('explain')).toContainText('Turn: room reply');
});

test('room modes, the scheduler and previews report what the core says', async ({ page }) => {
  await createProject(page, 'gocalc-room');
  await createAgent(page, 'Dana', 'backend');
  await openRoom(page, 'general');
  await page.getByTestId('room-mode').click();
  const panel = page.getByTestId('room-agents');
  await choose(panel.getByLabel('Mode', { exact: true }), 'sequential');
  await expect(panel.getByTestId('mode-text')).toContainText('one turn each per round');
  await panel.getByRole('button', { name: 'Add Dana' }).click();
  await panel.getByRole('button', { name: 'Save mode' }).click();
  await expect(panel).toContainText('Current mode: sequential');
  await page.reload();
  await openRoom(page, 'general');
  await page.getByTestId('room-mode').click();
  await expect(page.getByTestId('room-agents')).toContainText('Current mode: sequential');
  // moderator mode needs a moderator: the core refuses and says so
  await choose(page.getByTestId('room-agents').getByLabel('Mode', { exact: true }), 'moderator');
  await page.getByTestId('room-agents').getByRole('button', { name: 'Save mode' }).click();
  await expect(page.getByRole('alert')).toContainText('moderator');

  await projectNav(page, 'nav-tasks');
  await expect(page.getByTestId('scheduler')).toContainText('manual');
  await page.getByRole('button', { name: 'Start ready tasks now' }).click();
  await expect(page.getByTestId('tick-result')).toContainText('Started 0');
});

test('integrations: connecting stores the key without ever showing it; with no vendor server the test does not pass; disconnect removes it', async ({ page }) => {
  await createProject(page, 'gocalc-int');
  await workspaceNav(page, 'nav-integrations');
  const linear = page.getByRole('navigation', { name: 'All integrations' }).locator('[data-testid=integration-row][data-kind=linear]');
  await expect(linear).toContainText('Set up');
  await linear.click();
  const card = page.locator('[data-testid=integration][data-kind=linear]');
  const secret = 'lin_api_e2e_secret_value';
  await card.getByLabel('API key', { exact: true }).fill(secret);
  // no vendor server here: a port on this computer that nothing listens on (the real API is never called by the suite)
  await card.getByText('Advanced').click();
  await card.getByLabel('API URL').fill('http://127.0.0.1:1');
  await card.getByRole('button', { name: 'Connect Linear' }).click();
  await expect(card.getByTestId('connection-identity')).toContainText('stored in the credential store (never shown)');
  expect(await page.content()).not.toContain(secret);
  // the connection cannot be verified, and says so
  await card.getByRole('button', { name: 'Test', exact: true }).click();
  await expect(card.getByTestId('int-report')).toHaveAttribute('data-ok', 'false');
  await expect(card.getByTestId('int-report')).toContainText('could not reach Linear');
  // it is assigned to the project like any other, on the project's own Integrations page, and a rule there starts disabled and in dry-run
  await projectNav(page, 'nav-project-integrations', 'gocalc-int');
  const mine = page.locator('[data-testid=project-integration][data-kind=linear]');
  await mine.getByRole('button', { name: 'Assign to gocalc-int' }).click();
  await mine.getByTestId('assign-form').getByRole('button', { name: 'Assign', exact: true }).click();
  const row = mine.getByTestId('assignment');
  await expect(row).toContainText('gocalc-int');
  await row.getByRole('button', { name: 'Items' }).click();
  await expect(row.getByText('No items yet. Run a sync.')).toBeVisible();
  await row.getByRole('button', { name: 'Rules' }).click();
  await expect(row.getByText(/starts disabled and in dry-run/)).toBeVisible();
  await row.getByLabel('Rule name').fill('Open bugs');
  await row.getByRole('button', { name: 'Add rule (starts disabled, dry-run)' }).click();
  const rule = row.getByTestId('int-rule');
  await expect(rule).toContainText('disabled');
  await expect(rule).toContainText('dry-run');
  await workspaceNav(page, 'nav-integrations');
  await linear.click();
  // the connection names the project it is assigned to
  await expect(card.getByTestId('integration-project').filter({ hasText: 'gocalc-int' })).toBeVisible();
  // Disconnect is in the detail's … menu and asks first
  await card.getByRole('button', { name: 'More for Linear' }).click();
  await page.getByRole('menuitem', { name: 'Disconnect', exact: true }).click();
  await card.getByRole('button', { name: /Yes, disconnect/ }).click();
  await expect(card.getByTestId('detail-status')).toHaveText('Not connected');
  await expect(linear).toContainText('Set up');
});
