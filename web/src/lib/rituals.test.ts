import { describe, expect, it } from 'vitest';
import type { Today } from './api';
import { ritualOver } from './rituals';

const day = { date: '2026-10-06', pace: 3, started_at: '', wrapped_at: null, tomorrow_note: '', summary: '' };
const plan = { id: 1, opening: '', entries: [], fallback: false };
const today = (over: Partial<Today>): Today => ({
	date: '2026-10-06',
	day: null,
	plan: null,
	unplanned: [],
	blocked: [],
	...over
});

describe('ritualOver', () => {
	it.each([
		['the morning, once another tab has started the day', 'morning', today({ day, plan }), true],
		['the morning, while the day still has no plan', 'morning', today({}), false],
		['wrap-up, once another tab has wrapped the day', 'wrap', today({ day: { ...day, wrapped_at: '2026-10-06T23:00:00Z' }, plan }), true],
		['wrap-up, while the day is still open', 'wrap', today({ day, plan }), false]
	] as const)('Should tell a finished ritual from a changed task: %s', (_, ritual, t, want) => {
		expect(ritualOver(ritual, t)).toBe(want);
	});
});
