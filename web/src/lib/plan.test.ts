import { describe, expect, it } from 'vitest';
import type { Status, Task, Today } from './api';
import { doneOn, tiers, unplanned, winLine } from './plan';

const task = (id: number, status: Status = 'open', updated_at = '2026-10-05T15:00:00Z'): Task => ({
	id,
	title: `task ${id}`,
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	not_before: null,
	status,
	updated_at
});

const today = (over: Partial<Today> = {}): Today => ({
	date: '2026-10-05',
	day: { date: '2026-10-05', pace: 3 },
	plan: {
		id: 1,
		opening: '',
		fallback: false,
		entries: [
			{ task_id: 1, tier: 'must', rationale: 'due today' },
			{ task_id: 2, tier: 'should', rationale: 'r2' },
			{ task_id: 3, tier: 'must', rationale: 'r3' },
			{ task_id: 4, tier: 'could', rationale: 'gone' }
		]
	},
	unplanned: [],
	blocked: [],
	...over
});

describe('tiers', () => {
	it('Should group open plan tasks by tier in plan order, with blocked marked', () => {
		const got = tiers(today({ blocked: [3] }), [task(1), task(2), task(3)]);
		expect(got.must.map((c) => [c.task.id, c.blocked])).toEqual([
			[1, false],
			[3, true]
		]);
		expect(got.should[0].rationale).toBe('r2');
		expect(got.could).toEqual([]); // task 4 was deleted, so it isn't listed
	});

	it('Should leave out plan tasks finished or dropped since', () => {
		const got = tiers(today(), [task(1, 'done'), task(2, 'dropped'), task(3)]);
		expect(got.must.map((c) => c.task.id)).toEqual([3]);
		expect(got.should).toEqual([]);
	});

	it('Should leave out a plan task pushed past today, but keep one due back today', () => {
		const got = tiers(today(), [
			{ ...task(1), not_before: '2026-10-06' },
			{ ...task(3), not_before: '2026-10-05' }
		]);
		expect(got.must.map((c) => c.task.id)).toEqual([3]);
	});

	it('Should be empty when there is no plan yet', () => {
		expect(tiers(today({ plan: null }), [task(1)])).toEqual({ must: [], should: [], could: [] });
	});
});

describe('unplanned', () => {
	it('Should keep the server order and skip tasks it no longer has', () => {
		const got = unplanned(today({ unplanned: [9, 7, 8], blocked: [7] }), [task(7), task(9)]);
		expect(got.map((c) => [c.task.id, c.blocked])).toEqual([
			[9, false],
			[7, true]
		]);
	});
});

describe('doneOn', () => {
	it('Should list tasks done on that local date only', () => {
		const noonToday = new Date('2026-10-05T12:00:00').toISOString();
		const noonYesterday = new Date('2026-10-04T12:00:00').toISOString();
		const got = doneOn('2026-10-05', [
			task(1, 'done', noonToday),
			task(2, 'done', noonYesterday),
			task(3, 'open', noonToday)
		]);
		expect(got.map((t) => t.id)).toEqual([1]);
	});
});

describe('winLine', () => {
	it.each([
		[0, 0, 'Nothing checked off yet'],
		[1, 0, '✓ 1 thing done'],
		[6, 3, '✓ 6 things done, 3 meetings survived'],
		[0, 1, '✓ 1 meeting survived']
	])('Should read well for %i done and %i meetings', (done, meetings, want) => {
		expect(winLine(done, meetings)).toBe(want);
	});
});
