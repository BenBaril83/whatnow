import type { Settings } from './api';

/**
 * Whether opening `path` should go to onboarding first: setup was never finished or skipped, and
 * nothing is set up. Onboarding and Settings are where setup happens, so they never redirect.
 */
export function welcomeDue(s: Settings, path: string): boolean {
	return !s.onboarded && s.provider === '' && path !== '/welcome' && !path.startsWith('/settings');
}
