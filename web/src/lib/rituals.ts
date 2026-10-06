import type { Today } from './api';

/**
 * Whether a ritual is already over, so a 409 from it means another tab finished it rather than a
 * task changing under its list. The server answers 409 for both.
 */
export function ritualOver(ritual: 'morning' | 'wrap', today: Today): boolean {
	return ritual === 'morning' ? today.plan !== null : today.day?.wrapped_at != null;
}
