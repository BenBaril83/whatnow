import { describe, expect, it } from 'vitest';
import { ApiError, type Session } from '#lib/api.ts';
import { sessionEnded, showLanding, signInURL } from '#lib/session.svelte.ts';

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

describe('sessionEnded', () => {
	it('Should be true for a 401 in hosted mode', () => {
		expect(sessionEnded(signedIn, new ApiError(401, 'sign in first'))).toBe(true);
	});

	it('Should never be true in local mode', () => {
		expect(sessionEnded({ hosted: false, user: null }, new ApiError(401, 'no'))).toBe(false);
	});

	it.each([
		['another status', new ApiError(500, 'oops')],
		['an error that is not from the API', new Error('401')],
	])('Should be false for %s', (_, err) => {
		expect(sessionEnded(signedIn, err)).toBe(false);
	});

	it('Should be false before the session is known', () => {
		expect(sessionEnded(null, new ApiError(401, 'sign in first'))).toBe(false);
	});
});
