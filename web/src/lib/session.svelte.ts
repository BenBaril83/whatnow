import { api, ApiError, type Session } from '#lib/api.ts';

/** Who is using the page, known once the root layout has asked the server. */
class SessionState {
	value = $state<Session | null>(null);

	async load(): Promise<void> {
		this.value = await api.session();
	}
}

export const session = new SessionState();

/** Sign-in is a full-page navigation to the server, which sends the visitor's zone along. */
export function signInURL(tz: string): string {
	return `/auth/google?tz=${encodeURIComponent(tz)}`;
}

/** A signed-out visitor to a hosted server sees the landing page, except on the privacy page. */
export function showLanding(s: Session, path: string): boolean {
	return s.hosted && s.user === null && path !== '/privacy';
}

/**
 * A hosted session can end mid-page, when it expires or the account is deleted elsewhere. The
 * server then answers 401, and the visitor belongs back at sign-in rather than in a pile of toasts.
 */
export function sessionEnded(s: Session | null, err: unknown): boolean {
	return s?.hosted === true && err instanceof ApiError && err.status === 401;
}
