import type { Page } from '@playwright/test';
import { expect, fake, setUp, startDay, test } from './fixtures';

const tier = (page: Page, name: string) =>
	page.locator('section').filter({ has: page.getByRole('heading', { name, exact: true }) });

test('a capture splits into tasks, and undo puts the note back', async ({ page, request }) => {
	await setUp(request);
	await startDay(request, '');
	await page.goto('/');

	await page.getByLabel('Capture').fill('buy milk and call mom');
	await page.getByLabel('Capture').press('Enter');

	const toast = page.getByRole('status').filter({ hasText: 'Split your note into 2 tasks' });
	await expect(toast).toBeVisible();
	await expect(page.getByText('buy milk', { exact: true })).toBeVisible();
	await expect(page.getByText('call mom', { exact: true })).toBeVisible();

	await toast.getByRole('button', { name: 'Undo' }).click();
	await expect(page.getByText('buy milk and call mom', { exact: true })).toBeVisible();
	await expect(page.getByText('call mom', { exact: true })).toHaveCount(0);
});

test('good morning: a dump becomes a plan in tiers, and Replan takes in a new task', async ({ page, request }) => {
	await setUp(request);
	await page.goto('/');
	await expect(page).toHaveURL(/\/morning$/);

	await page.getByLabel("What's on your plate").fill('buy milk, call mom, water the plants');
	await page.getByRole('button', { name: 'Next →' }).click();
	await page.getByRole('button', { name: 'gentle' }).click();
	await page.getByRole('button', { name: 'Plan my day →' }).click();

	await expect(page.getByText(fake.opening)).toBeVisible();
	await page.getByRole('button', { name: "Let's go →" }).click();
	await expect(page).toHaveURL(/\/$/);
	await expect(tier(page, 'must').getByText('buy milk', { exact: true })).toBeVisible();
	await expect(tier(page, 'should').getByText('call mom', { exact: true })).toBeVisible();
	await expect(tier(page, 'could').getByText('water the plants', { exact: true })).toBeVisible();

	await page.getByLabel('Capture').fill('feed the cat');
	await page.getByLabel('Capture').press('Enter');
	await expect(tier(page, 'Unplanned').getByText('feed the cat', { exact: true })).toBeVisible();
	await page.getByRole('button', { name: 'Replan' }).click();
	await expect(tier(page, 'could').getByText('feed the cat', { exact: true })).toBeVisible();
});

test('what now picks the first free task, skips a pass, and done ticks it off', async ({ page, request }) => {
	await setUp(request);
	await startDay(request, 'buy milk and call mom');
	await page.goto('/');

	await page.getByRole('button', { name: 'What now?' }).click();
	const pick = page.locator('section', { hasText: 'Do this now' });
	await expect(pick.getByRole('heading', { name: 'buy milk' })).toBeVisible();
	await expect(pick.getByText(fake.reason)).toBeVisible();

	await pick.getByRole('button', { name: 'Not this one' }).click();
	await pick.getByRole('button', { name: 'Ask again' }).click();
	await expect(pick.getByRole('heading', { name: 'call mom' })).toBeVisible();

	await pick.getByRole('button', { name: 'Done' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Done: call mom' })).toBeVisible();
	await expect(pick).toHaveCount(0);
});
