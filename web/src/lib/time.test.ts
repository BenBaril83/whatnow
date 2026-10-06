import { describe, expect, it } from 'vitest';
import type { CalendarEvent } from './api';
import { addDays, countdown, morningDue, strip, survived, wrapDue } from './time';

const at = (hhmm: string) => new Date(`2026-10-05T${hhmm}:00`);
const ev = (title: string, start: string, end: string | null = null): CalendarEvent => ({
	id: 0,
	title,
	starts_at: at(start).toISOString(),
	ends_at: end ? at(end).toISOString() : null,
	created_at: at('08:00').toISOString()
});

describe('countdown', () => {
	const events = [ev('Standup', '09:30', '09:45'), ev('1:1', '13:00', '13:30')];

	it.each([
		['before the first event', '09:06', 'Standup in 24m'],
		['during an event', '09:40', 'Standup now'],
		['between events, over an hour out', '11:55', '1:1 in 1h 5m'],
		['an exact hour out', '12:00', '1:1 in 1h'],
		['after the last event', '14:00', null]
	])('Should say the right thing %s', (_, now, want) => {
		expect(countdown(events, at(now))).toBe(want);
	});

	it('Should treat an event with no end as 30 minutes long', () => {
		const open = [ev('Focus', '10:00')];
		expect(countdown(open, at('10:29'))).toBe('Focus now');
		expect(countdown(open, at('10:30'))).toBeNull();
	});

	it('Should find the next event when events arrive out of order', () => {
		expect(countdown([ev('Late', '15:00'), ev('Soon', '10:00')], at('09:00'))).toBe('Soon in 1h');
	});

	it('Should say nothing with no events', () => {
		expect(countdown([], at('09:00'))).toBeNull();
	});
});

describe('strip', () => {
	const eod = at('17:30');
	const labels = (s: ReturnType<typeof strip>) => s.ticks.map((t) => t.label);

	it('Should start at the day start, so the now line moves across as the day goes on', () => {
		const early = strip([], at('09:00'), eod, at('08:40').toISOString());
		const later = strip([], at('13:00'), eod, at('08:40').toISOString());
		expect(early.now).toBeGreaterThan(0);
		expect(later.now).toBeGreaterThan(early.now);
	});

	it('Should round the window out to whole hours, from 8:00 to 18:00', () => {
		const s = strip([], at('13:00'), eod, at('08:40').toISOString());
		expect(s.now).toBe(50); // 13:00 is halfway through 8:00 to 18:00
		expect(s.endOfDay).toBe(95); // 17:30
	});

	it('Should start at the first event when it is earlier than the day start', () => {
		const s = strip([ev('Standup', '07:30', '08:00')], at('13:00'), eod, at('08:40').toISOString());
		expect(s.items[0].left).toBeCloseTo(4.55, 1); // 30 of 660 minutes, from 7:00
	});

	it('Should start at now with no start and no earlier event', () => {
		const s = strip([], at('09:30'), eod, null);
		expect(s.now).toBeCloseTo(5.56, 1); // 30 of 540 minutes, from 9:00 to 18:00
	});

	it('Should draw an event with no end as 30 minutes', () => {
		const s = strip([ev('Focus', '13:00')], at('09:00'), at('17:00'), at('09:00').toISOString());
		expect(s.items[0].width).toBeCloseTo(5.56, 1); // 30 of 540 minutes
	});

	it('Should stretch past the end of day to keep now and late events on it', () => {
		const late = strip([ev('Late call', '18:00', '19:00')], at('09:00'), at('17:00'), at('09:00').toISOString());
		expect(late.items[0].left + late.items[0].width).toBeLessThanOrEqual(100);
		expect(late.endOfDay).toBeLessThan(late.items[0].left);
		const after = strip([], at('20:00'), at('17:00'), at('09:00').toISOString());
		expect(after.now).toBeLessThan(100);
	});

	it('Should tick every third clock hour on a long strip', () => {
		expect(labels(strip([], at('09:00'), eod, at('08:40').toISOString()))).toEqual(['9a', '12p', '3p']);
	});

	it('Should tick every hour on a strip of 6 hours or less', () => {
		expect(labels(strip([], at('13:10'), eod, at('13:05').toISOString()))).toEqual(['2p', '3p', '4p', '5p']);
	});

	it('Should label noon 12p', () => {
		expect(labels(strip([], at('11:10'), at('14:00'), at('11:05').toISOString()))).toContain('12p');
	});
});

describe('survived', () => {
	it('Should count only events that have ended', () => {
		const events = [ev('A', '09:00', '09:30'), ev('B', '09:45'), ev('C', '11:00', '12:00')];
		expect(survived(events, at('10:15'))).toBe(2);
	});
});

describe('addDays', () => {
	it.each([
		['2026-10-05', '2026-10-06'],
		['2026-10-31', '2026-11-01'],
		['2026-12-31', '2027-01-01'],
		['2026-03-07', '2026-03-08'] // the night before a US DST change
	])('Should give the day after %s', (date, want) => {
		expect(addDays(date, 1)).toBe(want);
	});
});

describe('wrapDue', () => {
	it.each([
		['16:29', false],
		['16:30', true],
		['20:00', true]
	])('Should offer wrap-up at %s with a 17:30 end of day: %s', (now, want) => {
		expect(wrapDue(at(now), at('17:30'))).toBe(want);
	});
});

describe('morningDue', () => {
	it.each([
		['the date changed and today has no plan', '2026-10-05', '2026-10-06', false, true],
		['the date changed but today already has a plan', '2026-10-05', '2026-10-06', true, false],
		['the same date with no plan', '2026-10-06', '2026-10-06', false, false]
	])('Should know whether to go to the morning when %s', (_, seen, date, planned, want) => {
		expect(morningDue(seen, date, planned)).toBe(want);
	});
});
