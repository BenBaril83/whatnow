import { createServer } from 'node:http';
import { expect, setUp, test } from './fixtures';

/** Where the e2e server's Linear points, from web/playwright.config.ts. */
const addr = { host: '127.0.0.1', port: 7790 };

/** The default pull: one issue of the user's and one up for grabs, with two more behind them. */
const answer = JSON.stringify({
	data: {
		viewer: {
			shown: {
				nodes: [
					{ id: 'a1', identifier: 'ENG-1', title: 'Mine', description: 'See https://example.com', url: 'https://linear.app/acme/issue/ENG-1', assignee: { id: 'u1' } },
					{ id: 'a2', identifier: 'ENG-2', title: 'Up for grabs', description: null, url: 'https://linear.app/acme/issue/ENG-2', assignee: null }
				]
			},
			rest: { nodes: [{ id: 'b1' }, { id: 'b2' }] }
		}
	}
});

test('Linear issues are picked in the morning, and neither a pick nor a skip comes back', async ({ page, request }) => {
	const auth: (string | undefined)[] = [];
	const server = createServer((req, res) => {
		auth.push(req.headers.authorization);
		res.setHeader('Content-Type', 'application/json');
		res.end(answer);
	});
	await new Promise<void>((resolve) => server.listen(addr.port, addr.host, resolve));
	try {
		await setUp(request);

		await page.goto('/settings/integrations');
		const card = page.locator('.card', { hasText: 'Linear' });
		await card.getByRole('button', { name: /Linear/ }).click();
		await card.getByLabel('API key').fill('lin_api_e2e');
		await card.getByRole('button', { name: 'Save' }).click();
		await expect(page.getByText('Saved Linear — switch it on to pull')).toBeVisible();
		await card.getByRole('switch').check();
		await expect(card.getByRole('button', { name: 'Pull', exact: true })).toBeEnabled();

		await page.goto('/morning');
		const chip = page.getByRole('button', { name: '↻ Linear' });
		await chip.click();
		await expect(page.getByText('Linear · 2')).toBeVisible();
		expect(auth).toEqual(['lin_api_e2e']);
		await page.getByRole('button', { name: 'Skip ENG-2' }).click();
		await page.getByRole('checkbox', { name: 'Pick ENG-1' }).check();
		await page.getByRole('button', { name: 'Add 1' }).click();
		await expect(page.getByText('Added 1.')).toBeVisible();

		await chip.click();
		await expect(page.getByText('Nothing new in Linear.')).toBeVisible();
	} finally {
		server.close();
	}
});
