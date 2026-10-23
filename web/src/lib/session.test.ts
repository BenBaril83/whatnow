import { describe, expect, it } from 'vitest';
import type { Session } from '#lib/api.ts';
import { showLanding, signInURL } from '#lib/session.svelte.ts';

const signedOut: Session = { hosted: true, user: null };
const signedIn: Session = {
	hosted: true,
	user: { name: 'Ada', email: 'ada@example.com' },
};

describe('showLanding', () => {
	it('Should never show in local mode', () => {
		expect(showLanding({ hosted: false, user: null }, '/')).toBe(false);
	});

	it.each(['/', '/settings'])(
		'Should show to a signed-out visitor on %s',
		(path) => {
			expect(showLanding(signedOut, path)).toBe(true);
		},
	);

	it('Should let a signed-out visitor read the privacy page', () => {
		expect(showLanding(signedOut, '/privacy')).toBe(false);
	});

	it('Should not show to a signed-in user', () => {
		expect(showLanding(signedIn, '/')).toBe(false);
	});
});

describe('signInURL', () => {
	it('Should carry the time zone, encoded', () => {
		expect(signInURL('America/New_York')).toBe(
			'/auth/google?tz=America%2FNew_York',
		);
	});
});
