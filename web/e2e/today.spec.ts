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

test('clicking a card opens its notes in full', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('button', { name: '+ Task' }).click();
	const sheet = page.getByRole('dialog', { name: 'New task' });
	await sheet.getByLabel('Title').fill('pack');
	await sheet.getByLabel('Notes').fill('socks\nshirts\ncharger');
	await sheet.getByRole('button', { name: 'Save' }).click();
	await expect(sheet).toBeHidden();

	const notes = page.locator('article', { hasText: 'pack' }).locator('.notes');
	const collapsed = (await notes.boundingBox())!.height;
	await page.locator('article', { hasText: 'pack' }).getByRole('button', { name: /pack/ }).click();
	await expect.poll(async () => (await notes.boundingBox())!.height).toBeGreaterThan(collapsed * 2);
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
	// The goodnight screen shows the reflection; the summary is the planner's, not the user's.
	await expect(page.getByText(fake.reflection)).toBeVisible();
	await expect(page.getByText(fake.sentence)).toHaveCount(0);

	// Day's done links the source everywhere, but asks for tips only on the hosted server.
	await page.getByRole('button', { name: 'Goodnight →' }).click();
	await expect(page.getByRole('link', { name: 'source on GitHub' })).toBeVisible();
	await expect(page.getByRole('link', { name: /Ko-fi/ })).toHaveCount(0);
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

test("an event's × removes it from the strip, and Undo brings it back", async ({ page, request }) => {
	const startsAt = new Date(Date.now() + 5 * 60 * 1000).toISOString();
	await request.post('/api/v1/events', { data: { title: 'Dentist', starts_at: startsAt, ends_at: null, raw_text: '' } });
	await page.goto('/');
	const strip = page.getByLabel("Today's events");
	await expect(strip.getByText('Dentist')).toBeVisible();

	await strip.getByText('Dentist').hover();
	await page.getByRole('button', { name: 'Remove Dentist' }).click();
	await expect(strip.getByText('Dentist')).toHaveCount(0);

	await page.getByRole('button', { name: 'Undo' }).click();
	await expect(strip.getByText('Dentist')).toBeVisible();
});

test('Move puts a card in another tier, or the tray', async ({ page }) => {
	await page.goto('/');
	const must = page.locator('section').filter({ has: page.getByRole('heading', { name: 'must' }) });
	const card = page.locator('article').filter({ hasText: 'call mom' });

	await card.getByLabel('More actions').click();
	await card.getByText('Move ›').click();
	await expect(card.getByRole('button', { name: 'Should' })).toBeDisabled();
	await card.getByRole('button', { name: 'Must' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Moved to Must: call mom' })).toBeVisible();
	await expect(must.getByText('call mom')).toBeVisible();

	await card.getByLabel('More actions').click();
	await card.getByText('Move ›').click();
	await card.getByRole('button', { name: 'Unplanned' }).click();
	await expect(page.locator('section').filter({ has: page.getByRole('heading', { name: 'Unplanned' }) }).getByText('call mom')).toBeVisible();
});
