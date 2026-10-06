import { test as base, expect, type APIRequestContext, type APIResponse } from '@playwright/test';

/** The fake model's fixed text, from internal/llm/fake. */
export const fake = {
	opening: 'A fake plan, in order.',
	reason: 'First free task.',
	suggest: 'Keep it going.',
	sentence: 'A fake summary.',
	chatReply: 'A fake reply. Nothing changed.'
};

async function ok(res: Promise<APIResponse>): Promise<APIResponse> {
	const r = await res;
	expect(r.ok(), `${r.url()}: ${r.status()}`).toBe(true);
	return r;
}

/** Sets up a provider and a key, as finishing onboarding would. */
export async function setUp(request: APIRequestContext): Promise<void> {
	const view = await (await ok(request.get('/api/v1/settings'))).json();
	await ok(request.put('/api/v1/settings', { data: { ...view.settings, provider: 'anthropic', onboarded: true } }));
	await ok(request.put('/api/v1/settings/keys/anthropic', { data: { key: 'e2e' } }));
}

/** Starts today. The fake splits the dump into tasks and plans them in order. */
export async function startDay(request: APIRequestContext, dump: string): Promise<void> {
	await ok(request.post('/api/v1/today/start', { data: { pace: 3, mood_note: '', triage: [], dump } }));
}

/** Every test starts from a wiped install, as after Settings' delete everything. */
export const test = base.extend<{ fresh: void }>({
	fresh: [
		async ({ request }, use) => {
			// The API refuses writes that aren't JSON, so a page on another site can't post forms to it.
			await ok(request.delete('/api/v1/data', { headers: { 'Content-Type': 'application/json' } }));
			await use();
		},
		{ auto: true }
	]
});

export { expect };
