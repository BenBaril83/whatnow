import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, setUp, test } from './fixtures';

/** A search with one ticket of the user's and one up for grabs. */
const search = JSON.stringify({
	total: 2,
	issues: [
		{ key: 'FLEX1-1', fields: { summary: 'Mine', description: 'See https://example.com', assignee: { name: 'me' } } },
		{ key: 'FLEX1-2', fields: { summary: 'Up for grabs', description: null, assignee: null } }
	]
});

test('Jira tickets are picked in the morning, and neither a pick nor a skip comes back', async ({ page, request }) => {
	const server = createServer((_, res) => {
		res.setHeader('Content-Type', 'application/json');
		res.end(search);
	});
	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
	try {
		await setUp(request);

		await page.goto('/settings/integrations');
		const card = page.locator('.card', { hasText: 'Jira' });
		await card.getByRole('button', { name: /Jira/ }).click();
		await card.getByLabel('Address').fill(url);
		const token = card.getByLabel('Personal access token');
		await expect(token).toHaveAttribute('placeholder', '');
		await token.fill('pat');
		await card.getByLabel('Search (JQL)').fill('project = FLEX1');
		await card.getByRole('button', { name: 'Save' }).click();
		await expect(card.getByText('✓ Saved')).toBeVisible();
		await expect(page.getByText('Saved Jira — switch it on to pull')).toBeVisible();
		await card.getByRole('button', { name: /Jira/ }).click();
		await expect(token).toHaveValue('');
		await expect(token).toHaveAttribute('placeholder', 'Leave blank to keep');
		await card.getByRole('switch').check();
		await expect(card.getByRole('button', { name: 'Pull', exact: true })).toBeEnabled();

		await page.goto('/morning');
		const chip = page.getByRole('button', { name: '↻ Jira' });
		await chip.click();
		await expect(page.getByText('Jira · 2')).toBeVisible();
		await page.getByRole('button', { name: 'Skip FLEX1-2' }).click();
		await page.getByRole('checkbox', { name: 'Pick FLEX1-1' }).check();
		await page.getByRole('button', { name: 'Add 1' }).click();
		await expect(page.getByText('Added 1.')).toBeVisible();

		await chip.click();
		await expect(page.getByText('Nothing new in Jira.')).toBeVisible();
	} finally {
		server.close();
	}
});
