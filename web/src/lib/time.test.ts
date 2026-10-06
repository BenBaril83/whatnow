import { describe, expect, it } from 'vitest';
import type { CalendarEvent } from './api';
import { addDays, countdown, strip, survived } from './time';

const at = (hhmm: string) => new Date(`2026-10-05T${hhmm}:00`);
const ev = (title: string, start: string, end: string | null = null): CalendarEvent => ({
	id: 0,
	title,
	starts_at: at(start).toISOString(),
	ends_at: end ? at(end).toISOString() : null
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

	it('Should run from now to the end of day with no events', () => {
		const s = strip([], at('09:30'), eod);
		expect(s.items).toEqual([]);
		expect(s.now).toBe(0);
	});

	it('Should start at the first event when it is earlier than now', () => {
		const s = strip([ev('Standup', '09:00', '09:30')], at('13:00'), at('17:00'));
		expect(s.items[0].left).toBe(0);
		expect(s.items[0].width).toBeCloseTo(6.25); // 30 of 480 minutes
		expect(s.now).toBe(50);
	});

	it('Should draw an event with no end as 30 minutes', () => {
		const s = strip([ev('Focus', '13:00')], at('09:00'), at('17:00'));
		expect(s.items[0].width).toBeCloseTo(6.25);
	});

	it('Should stretch past the end of day to keep now and late events on it', () => {
		const late = strip([ev('Late call', '18:00', '19:00')], at('09:00'), at('17:00'));
		expect(late.items[0].left + late.items[0].width).toBeCloseTo(100);
		const after = strip([], at('20:00'), at('17:00'));
		expect(after.now).toBe(0);
		expect(Number.isFinite(after.now)).toBe(true);
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
