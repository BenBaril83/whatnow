import { expect, fake, setUp, startDay, test } from './fixtures';

test.beforeEach(async ({ request }) => {
	await setUp(request);
	await startDay(request, 'buy milk and call mom');
});

test('menus close on an outside click and on Escape', async ({ page }) => {
	await page.goto('/');
	const allTasks = page.getByRole('link', { name: 'All tasks' });

	await page.getByLabel('Menu').click();
	await expect(allTasks).toBeVisible();
	await page.getByRole('heading', { name: 'must' }).click();
	await expect(allTasks).toBeHidden();

	await page.getByLabel('Menu').click();
	await expect(allTasks).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(allTasks).toBeHidden();

	const push = page.getByRole('button', { name: 'Push to tomorrow' }).first();
	await page.getByLabel('More actions').first().click();
	await expect(push).toBeVisible();
	await page.getByRole('heading', { name: 'must' }).click();
	await expect(push).toBeHidden();
});

test('+ Task makes a blocked task, and its card links to the blocker', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('button', { name: '+ Task' }).click();
	const sheet = page.getByRole('dialog', { name: 'New task' });
	await sheet.getByLabel('Title').fill('pour the cereal');
	await sheet.getByLabel('Add a blocker').selectOption({ label: 'buy milk' });
	await sheet.getByRole('button', { name: 'Save' }).click();

	await expect(sheet).toBeHidden();
	await expect(page.getByRole('status').filter({ hasText: 'Added: pour the cereal' })).toBeVisible();
	await page.getByRole('button', { name: '⛓ waits on 1' }).click();
	await expect(page.getByText('Waiting on: buy milk')).toBeVisible();
});

test('chat streams a reply', async ({ page }) => {
	await page.goto('/');
	await page.getByLabel('Message').fill('hello');
	await page.getByLabel('Message').press('Enter');
	await expect(page.getByText(fake.chatReply)).toBeVisible();
});

test('wrap-up suggests rolling over what is left, and wraps the day', async ({ page }) => {
	await page.goto('/');
	await page.locator('article', { hasText: 'buy milk' }).getByLabel('Mark done').check();
	await expect(page.getByRole('status').filter({ hasText: 'Done: buy milk' })).toBeVisible();

	await page.getByLabel('Menu').click();
	await page.getByRole('link', { name: 'Wrap up early' }).click();
	await page.getByRole('button', { name: 'Next →' }).click();
	await expect(page.getByText(fake.suggest)).toBeVisible();
	await page.getByRole('button', { name: 'Next →' }).click();
	await page.getByRole('button', { name: 'Wrap up' }).click();

	await expect(page.getByRole('heading', { name: 'Goodnight 🌙' })).toBeVisible();
	await expect(page.getByText(fake.sentence)).toBeVisible();
});

test('the end-of-day label stays inside the strip on a phone', async ({ page, request }) => {
	const view = await (await request.get('/api/v1/settings')).json();
	await request.put('/api/v1/settings', { data: { ...view.settings, end_of_day: '23:59' } });
	await page.setViewportSize({ width: 375, height: 800 });
	await page.goto('/');

	const strip = await page.getByLabel("Today's events").boundingBox();
	const label = await page.getByText('11:59').boundingBox();
	expect(label!.x + label!.width).toBeLessThanOrEqual(strip!.x + strip!.width);
});
