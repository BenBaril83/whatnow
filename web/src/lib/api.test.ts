import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, configured, type SettingsView } from './api';

afterEach(() => vi.unstubAllGlobals());

function stubFetch(status: number, body: unknown) {
	const fetch = vi.fn(async () => new Response(status === 204 ? null : JSON.stringify(body), { status }));
	vi.stubGlobal('fetch', fetch);
	return fetch;
}

describe('api', () => {
	it('Should send every write as JSON, even with no body to send', async () => {
		const fetch = stubFetch(204, null);
		await api.undo(4);
		expect(fetch).toHaveBeenCalledWith('/api/v1/batches/4/undo', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: '{}'
		});
	});

	it("Should throw the server's message with its status", async () => {
		stubFetch(409, { error: 'a reply is still being written' });
		await expect(api.send('hi')).rejects.toEqual(new ApiError(409, 'a reply is still being written'));
	});
});

describe('configured', () => {
	const view = (provider: '' | 'anthropic', key: '' | 'env'): SettingsView => ({
		settings: {
			provider,
			anthropic: { main_model: '', light_model: '', base_url: '' },
			openai: { main_model: '', light_model: '', base_url: '' },
			style: '',
			about_me: '',
			end_of_day: '17:30',
			nudges: { before_event: true, event_end: true, late_day: true },
			alert_lead_minutes: 10,
			onboarded: true
		},
		keys: { anthropic: key, openai: '' }
	});

	it.each([
		['no provider', false, view('', 'env')],
		['a provider with no key', false, view('anthropic', '')],
		['a provider with a key', true, view('anthropic', 'env')]
	])('Should count %s as set up: %s', (_, want, v) => {
		expect(configured(v)).toBe(want);
	});
});
