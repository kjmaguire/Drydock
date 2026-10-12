import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createAssignedTask, createProject, docsDir, freshGreenRepo, roomLink } from './helpers';

// Slice 2-5 screens against the real Go core. The agent process is the scripted fake agent; this integrations test has no
// vendor server, so only the refusal paths are exercised (integrations.spec.ts runs against a protocol fixture).

test('economics: a budget is set and listed, the evals report renders, routing rules and a preview are shown, telemetry is off and local', async ({ page }) => {
  await createProject(page, 'gocalc-econ');

  // budgets
  await page.getByTestId('nav-budgets').click();
  await expect(page.getByText('No budgets')).toBeVisible();
  await page.getByRole('button', { name: 'Add budget' }).first().click();
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
  await page.getByLabel('Scope', { exact: true }).selectOption('task');
  await page.getByRole('button', { name: 'Add budget' }).last().click();
  await expect(page.getByRole('alert')).toContainText('set at least one of limitUsd, limitTokens, limitTurns');
  await page.reload();
  await expect(page.getByTestId('budget')).toHaveCount(1);

  // evals
  await page.getByTestId('nav-evals').click();
  await expect(page.getByTestId('evals-markdown')).toBeVisible();
  await expect(page.getByTestId('evals-headline')).toBeVisible();
  await expect(page.getByTestId('ratio-completion')).toHaveText('n/a'); // no task has started: a zero denominator is n/a
  await page.getByRole('button', { name: 'Export to a local file' }).click();
  await expect(page.getByTestId('evals-exported')).toContainText('stays on this machine');

  // routing
  await page.getByTestId('nav-routing').click();
  await expect(page.getByTestId('route-rule').first()).toBeVisible();
  await page.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(page.getByTestId('route-preview')).toContainText('Recorded: no');

  // telemetry: off by default, local only, a preview of what an export holds
  await page.getByRole('link', { name: 'Settings and About' }).click();
  const tel = page.getByTestId('telemetry');
  await expect(tel).toContainText('off');
  await expect(tel).toContainText('this device only');
  await tel.getByRole('button', { name: 'Preview what an export contains' }).click();
  await expect(page.getByTestId('telemetry-preview')).toContainText('allowlisted');
  await tel.getByRole('button', { name: 'Turn telemetry on' }).click();
  await expect(tel.getByRole('button', { name: 'Turn telemetry off' })).toBeVisible();
  await tel.getByRole('button', { name: 'Turn telemetry off' }).click();
  await expect(tel.getByRole('button', { name: 'Turn telemetry on' })).toBeVisible();

  // project settings: checks and the development flow
  await page.getByRole('link', { name: 'Project settings' }).click();
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
  await page.getByTestId('nav-packs').click();
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
  await page.getByTestId('nav-requirements').click();
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
  await page.getByTestId('nav-tasks').click();
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Build checkout');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Build checkout/ })).toBeVisible();
  await page.getByTestId('nav-requirements').click();
  await page.getByTestId('requirement').filter({ hasText: 'Checkout' }).getByRole('button', { name: 'Open' }).click();
  await page.getByLabel('Task to link').selectOption({ label: '#1 Build checkout' });
  await page.getByLabel(/^Criteria \(one per line/).fill('Given a paid cart, when the order is placed, then an order record exists');
  await page.getByRole('button', { name: 'Link' }).click();
  await expect(page.getByTestId('req-link')).toContainText('Given a paid cart');
  await expect(page.getByTestId('trace-task')).toContainText('Build checkout');
  await expect(page.getByTestId('trace-task')).toContainText('no test evidence');

  // milestones
  await page.getByTestId('nav-milestones').click();
  await expect(page.getByRole('heading', { name: 'New milestone' })).toBeVisible();
  await page.getByLabel('Title', { exact: true }).fill('Beta');
  await page.getByRole('button', { name: 'Create milestone' }).click();
  const ms = page.locator('[data-testid=milestone][data-title=Beta]');
  await expect(ms).toBeVisible();
  await ms.getByLabel('Add a task to Beta').selectOption({ label: '#1 Build checkout' });
  await ms.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(ms).toContainText('0 of 1 completed');

  // skills: repository skills are offered, never applied; the human accepts
  await page.getByTestId('nav-skills').click();
  await page.getByRole('button', { name: 'Scan the repository for skills' }).click();
  await expect(page.getByTestId('skill-scan')).toContainText('1 offered, 0 accepted');
  const skill = page.locator('[data-testid=skill][data-skill=go-style]');
  await expect(skill).toHaveAttribute('data-status', 'offered');
  await skill.getByRole('button', { name: 'Accept go-style' }).click();
  await expect(skill).toHaveAttribute('data-status', 'accepted');

  // memory: propose, accept, delete
  await page.getByTestId('nav-memory').click();
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
});

test('development flow: the Feature template stops at the spec gate; reject with feedback, accept with edited criteria, red then green, approve', async ({ page }) => {
  await createProject(page, 'gocalc-feature', freshGreenRepo());
  await createAgent(page, 'Priya', 'pm');
  await createAgent(page, 'Quinn', 'qa');
  await createAgent(page, 'Dana', 'backend');
  await createAgent(page, 'Rex', 'reviewer');
  await createAssignedTask(page, 'Add Multiply', 'Dana', 'Rex');
  await page.getByLabel('Template', { exact: true }).selectOption({ label: 'Feature' });
  await expect(page.getByTestId('template-info')).toContainText('accept the spec before anything is built');
  await page.getByRole('button', { name: 'Start' }).click();

  // the spec gate
  const gate = page.getByTestId('spec-gate');
  await expect(gate).toBeVisible({ timeout: 90_000 });
  await expect(gate.getByTestId('spec-criteria').locator('li')).toHaveCount(2);
  await expect(page.getByTestId('task-status').first()).toContainText('Waiting for you');
  // nothing has been built: no red or green evidence yet
  await expect(page.locator('[data-testid=evidence][data-kind=red_test]')).toHaveCount(0);
  // it is in Needs you with a plain hint
  await page.getByTestId('nav-needs-you').click();
  await expect(page.getByTestId('needs-item').first()).toContainText('Accept the spec');
  await expect(page.getByTestId('needs-hint').first()).toContainText('spec gate');
  await page.getByTestId('needs-item').first().getByRole('link').click();

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
  await page.getByTestId('nav-plan').click();
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
  await page.getByTestId('nav-tasks').click();
  await expect(page.getByTestId('task-row').filter({ hasText: 'Add Multiply' })).toHaveCount(1);
  await expect(page.getByTestId('task-row').filter({ hasText: 'Add Divide' })).toHaveCount(0);
});

test('a budget pause stops the next agent turn, appears in Needs you, and only the human resumes it', async ({ page }) => {
  await createProject(page, 'gocalc-budget');
  await createAgent(page, 'Worker B', 'backend');
  await createAgent(page, 'Reviewer B', 'reviewer');
  await page.getByTestId('nav-budgets').click();
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
  await page.getByTestId('nav-needs-you').click();
  await expect(page.getByTestId('needs-hint').first()).toContainText('budget stopped the next agent turn');
  await page.getByTestId('needs-item').first().getByRole('link').click();

  // raise the limit and resume: the run continues to the review panel
  await page.getByLabel('New turn limit').fill('10');
  await page.getByRole('button', { name: 'Resume' }).click();
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByTestId('budget-pause')).toHaveCount(0);
  await page.getByTestId('nav-budgets').click();
  await expect(page.getByTestId('budget').first()).toContainText('10 turns');
});

test('room modes, the scheduler and previews report what the core says', async ({ page }) => {
  await createProject(page, 'gocalc-room');
  await createAgent(page, 'Dana', 'backend');
  await roomLink(page, 'general').click();
  const panel = page.getByTestId('room-agents');
  await panel.locator('summary').click();
  await panel.getByLabel('Mode', { exact: true }).selectOption('sequential');
  await expect(panel.getByTestId('mode-text')).toContainText('one turn each per round');
  await panel.getByRole('button', { name: 'Add Dana' }).click();
  await panel.getByRole('button', { name: 'Save mode' }).click();
  await expect(panel).toContainText('Current mode: sequential');
  await page.reload();
  await roomLink(page, 'general').click();
  await page.getByTestId('room-agents').locator('summary').click();
  await expect(page.getByTestId('room-agents')).toContainText('Current mode: sequential');
  // moderator mode needs a moderator: the core refuses and says so
  await page.getByTestId('room-agents').getByLabel('Mode', { exact: true }).selectOption('moderator');
  await page.getByTestId('room-agents').getByRole('button', { name: 'Save mode' }).click();
  await expect(page.getByRole('alert')).toContainText('moderator');

  await page.getByTestId('nav-tasks').click();
  await expect(page.getByTestId('scheduler')).toContainText('manual');
  await page.getByRole('button', { name: 'Start ready tasks now' }).click();
  await expect(page.getByTestId('tick-result')).toContainText('Started 0');
});

test('integrations: connecting stores the key without ever showing it; with no vendor server the test does not pass; disconnect removes it', async ({ page }) => {
  await createProject(page, 'gocalc-int');
  await page.getByTestId('nav-integrations').click();
  await expect(page.getByText('Nothing is connected.')).toBeVisible();
  await page.locator('[data-testid=integration-tile][data-kind=linear]').click();
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
  // it is assigned to the project like any other, and a rule there starts disabled and in dry-run
  await card.getByRole('button', { name: 'Assign to project' }).click();
  await card.getByTestId('assign-form').getByLabel('Project', { exact: true }).selectOption({ label: 'gocalc-int' });
  await card.getByTestId('assign-form').getByRole('button', { name: 'Assign', exact: true }).click();
  const row = card.getByTestId('assignment');
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
  await card.getByRole('button', { name: 'Disconnect' }).click();
  await card.getByRole('button', { name: /Yes, disconnect/ }).click();
  await expect(page.getByText('Nothing is connected.')).toBeVisible();
});
