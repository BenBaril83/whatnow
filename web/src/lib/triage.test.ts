import { describe, expect, it } from 'vitest';
import type { Review, Task } from './api';
import { choicesFor, triagePayload } from './triage';

const task = (id: number): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: null,
	not_before: null,
	status: 'open',
	refs: null,
	created_at: '2026-10-01T15:00:00Z',
	updated_at: '2026-10-01T15:00:00Z'
});

const review = (ids: number[], over: Partial<Review> = {}): Review => ({
	day: { date: '2026-10-05', pace: 3, started_at: '', wrapped_at: null, tomorrow_note: '', summary: '' },
	done: [],
	unfinished: ids.map(task),
	suggestions: [
		{ task_id: 1, reason: 'due today', action: 'roll_over', until: '' },
		{ task_id: 2, reason: 'weekend job', action: 'push', until: '2026-10-10' },
		{ task_id: 3, reason: 'pushed 5 times', action: 'drop', until: '' }
	],
	fallback: false,
	...over
});

describe('choicesFor', () => {
	it('Should prefill each task from its suggestion', () => {
		expect(choicesFor(review([1, 2, 3]), '2026-10-06')).toEqual({
			1: { action: 'roll_over', until: '2026-10-06' },
			2: { action: 'push', until: '2026-10-10' },
			3: { action: 'drop', until: '2026-10-06' }
		});
	});

	it('Should roll over a task with no suggestion, ready to push to tomorrow', () => {
		expect(choicesFor(review([4]), '2026-10-06')).toEqual({ 4: { action: 'roll_over', until: '2026-10-06' } });
	});

	it("Should keep the user's choices for tasks still on a refetched review, and drop the rest", () => {
		const kept = { 1: { action: 'drop' as const, until: '2026-10-06' }, 9: { action: 'drop' as const, until: '2026-10-06' } };
		expect(choicesFor(review([1, 2]), '2026-10-06', kept)).toEqual({
			1: { action: 'drop', until: '2026-10-06' },
			2: { action: 'push', until: '2026-10-10' }
		});
	});
});

describe('triagePayload', () => {
	it('Should send until only for a push', () => {
		const got = triagePayload({
			1: { action: 'roll_over', until: '2026-10-06' },
			2: { action: 'push', until: '2026-10-10' },
			3: { action: 'drop', until: '2026-10-06' }
		});
		expect(got).toEqual([
			{ task_id: 1, action: 'roll_over', until: null },
			{ task_id: 2, action: 'push', until: '2026-10-10' },
			{ task_id: 3, action: 'drop', until: null }
		]);
	});

	it('Should send a push with a cleared date as tomorrow, by leaving until out', () => {
		expect(triagePayload({ 2: { action: 'push', until: '' } })).toEqual([
			{ task_id: 2, action: 'push', until: null }
		]);
	});
});
