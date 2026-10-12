import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, setUp, test } from './fixtures';

/** A date as an iCal local time, such as 20261005T150000. */
function icalTime(d: Date, hour: number): string {
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}T${pad(hour)}0000`;
}

/** Today's standup, and a daily blockout that started last week, in floating local time. */
function feed(): string {
	const today = new Date();
	const lastWeek = new Date(today.getTime() - 7 * 86_400_000);
	const event = (lines: string[]) => ['BEGIN:VEVENT', 'DTSTAMP:20260101T000000Z', ...lines, 'END:VEVENT'];
	return [
		'BEGIN:VCALENDAR',
		'VERSION:2.0',
		'X-WR-CALNAME:me@example.com',
		...event(['UID:standup', 'SUMMARY:Standup', `DTSTART:${icalTime(today, 10)}`, `DTEND:${icalTime(today, 11)}`]),
		...event(['UID:blockout', 'SUMMARY:Blockout', 'RRULE:FREQ=DAILY', `DTSTART:${icalTime(lastWeek, 15)}`, `DTEND:${icalTime(lastWeek, 16)}`]),
		'END:VCALENDAR',
		''
	].join('\r\n');
}

test('a calendar pulls into the morning, and always skip keeps a series out', async ({ page, request }) => {
	const server = createServer((_, res) => res.end(feed()));
	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/basic.ics`;
	try {
		await setUp(request);

		await page.goto('/settings/integrations');
		await page.getByRole('button', { name: /Google Calendar/ }).click();
		await page.getByRole('button', { name: '+ Add' }).click();
		await page.getByLabel('Name').fill('Work');
		await page.getByLabel('Secret address').fill(url);
		await page.getByRole('button', { name: 'Save' }).click();
		await expect(page.getByPlaceholder('•••••••• saved')).toBeVisible();
		await page.getByRole('switch').check();
		await expect(page.getByRole('button', { name: 'Pull today' })).toBeEnabled();

		await page.goto('/morning');
		const chip = page.getByRole('button', { name: '↻ Google Calendar' });
		await chip.click();
		await expect(page.getByText('Work · 2')).toBeVisible();
		await page.getByRole('button', { name: 'Remove Blockout' }).click();
		await page.getByRole('button', { name: 'Always skip' }).click();
		await expect(page.getByText('Work · 1')).toBeVisible();

		await chip.click();
		await expect(page.getByText('Standup', { exact: true })).toBeVisible();
		await expect(page.getByText('Work · 1')).toBeVisible();

		await page.goto('/settings/integrations');
		await page.getByRole('button', { name: /Google Calendar/ }).click();
		await expect(page.getByRole('button', { name: 'bring back' })).toBeVisible();
		await expect(page.getByText('Blockout')).toBeVisible();
	} finally {
		server.close();
	}
});
