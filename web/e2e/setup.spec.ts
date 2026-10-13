import { expect, setUp, startDay, test } from './fixtures';

test('onboarding walks a fresh install through to the morning', async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveURL(/\/welcome$/);
	const next = page.getByRole('button', { name: 'Next', exact: true });

	await expect(page.getByRole('heading', { name: "Hi, I'm whatnow" })).toBeVisible();
	await next.click();
	await page.getByLabel('API key').fill('e2e');
	await next.click();
	await expect(page.getByText('Connected.')).toBeVisible();
	await next.click();
	await page.getByRole('button', { name: 'Skip' }).click();
	await expect(page.getByRole('heading', { name: 'Bring in your work' })).toBeVisible();
	await expect(page.getByRole('button', { name: /Google Calendar/ })).toBeVisible();
	await expect(page.getByRole('button', { name: /Jira/ })).toBeVisible();
	await page.getByRole('button', { name: 'Skip' }).click();
	await next.click();
	await expect(page.getByRole('heading', { name: "You're set" })).toBeVisible();
	await page.getByRole('button', { name: 'Start my day' }).click();

	await expect(page).toHaveURL(/\/morning$/);
});

test('settings survive a reload, and delete everything starts over', async ({ page, request }) => {
	await setUp(request);
	await startDay(request, 'buy milk');
	await page.goto('/settings');

	const lead = page.getByLabel('Event alerts, minutes before');
	await lead.fill('25');
	await Promise.all([
		page.waitForResponse((r) => r.url().endsWith('/api/v1/settings') && r.request().method() === 'PUT'),
		page.getByRole('button', { name: 'Save' }).click()
	]);
	await page.reload();
	await expect(lead).toHaveValue('25');

	await page.getByLabel('Type delete everything to confirm').fill('delete everything');
	await page.getByRole('button', { name: 'Delete everything' }).click();
	await expect(page).toHaveURL(/\/welcome$/);
});

test('a picked theme applies at once and survives a reload and a later save', async ({ page, request }) => {
	await setUp(request);
	await page.goto('/settings');
	const html = page.locator('html');

	await Promise.all([
		page.waitForResponse((r) => r.url().endsWith('/api/v1/settings') && r.request().method() === 'PUT'),
		page.getByLabel('Dream of the 90s (plain)').check()
	]);
	await expect(html).toHaveAttribute('data-theme', 'plain');

	await page.getByLabel('Event alerts, minutes before').fill('25');
	await Promise.all([
		page.waitForResponse((r) => r.url().endsWith('/api/v1/settings') && r.request().method() === 'PUT'),
		page.getByRole('button', { name: 'Save' }).click()
	]);
	await page.reload();
	await expect(html).toHaveAttribute('data-theme', 'plain');

	await page.getByLabel('Sticker').check();
	await expect(html).not.toHaveAttribute('data-theme');
});
