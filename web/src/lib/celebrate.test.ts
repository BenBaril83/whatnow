import { describe, expect, it } from 'vitest';
import type { BatchApplied, Source, Status, Task, Today } from './api';
import { celebration } from './celebrate';

const task = (id: number, status: Status = 'open'): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: null,
	not_before: null,
	status,
	refs: null,
	created_at: '2026-10-01T15:00:00Z',
	updated_at: '2026-10-05T15:00:00Z'
});

const today: Today = {
	date: '2026-10-05',
	day: null,
	plan: {
		id: 1,
		opening: '',
		fallback: false,
		entries: [
			{ task_id: 1, tier: 'must', rationale: '' },
			{ task_id: 2, tier: 'should', rationale: '' },
			{ task_id: 3, tier: 'could', rationale: '' }
		]
	},
	unplanned: [4]
};

const change = (id: number, from: Status, to: Status) => ({
	action: 'status',
	entity: 'task',
	entity_id: id,
	before: { status: from },
	after: { status: to }
});

const batch = (activity: BatchApplied['activity'], source: Source = 'user'): BatchApplied => ({
	batch_id: 9,
	source,
	activity
});

const open = [task(1), task(2), task(3), task(4)];

describe('celebration', () => {
	it('Should give a small burst for a should, a could, or an unplanned task', () => {
		for (const id of [2, 3, 4]) {
			expect(celebration(batch([change(id, 'open', 'done')]), today, open)).toBe('small');
		}
	});

	it('Should give a bigger one for a must', () => {
		expect(celebration(batch([change(1, 'open', 'done')]), today, open)).toBe('must');
	});

	it('Should give the finale when the last open planned task is done', () => {
		const tasks = [task(1, 'done'), task(2, 'dropped'), task(3), task(4)];
		expect(celebration(batch([change(3, 'open', 'done')]), today, tasks)).toBe('finale');
	});

	it('Should give the finale when one batch finishes the rest of the plan', () => {
		const tasks = [task(1), task(2, 'done'), task(3), task(4)];
		const b = batch([change(1, 'open', 'done'), change(3, 'open', 'done')], 'chat');
		expect(celebration(b, today, tasks)).toBe('finale');
	});

	it('Should not give the finale again for an unplanned task after the plan is done', () => {
		const tasks = [task(1, 'done'), task(2, 'done'), task(3, 'done'), task(4)];
		expect(celebration(batch([change(4, 'open', 'done')]), today, tasks)).toBe('small');
	});

	it('Should give the biggest size when a batch finishes several tasks', () => {
		const b = batch([change(2, 'open', 'done'), change(1, 'open', 'done')]);
		expect(celebration(b, today, open)).toBe('must');
	});

	it('Should celebrate nothing that is not a completion', () => {
		const cases: BatchApplied[] = [
			batch([change(1, 'done', 'open')]),
			batch([change(1, 'open', 'dropped')]),
			batch([{ action: 'create', entity: 'task', entity_id: 5, before: null, after: { status: 'done' } }]),
			batch([change(1, 'open', 'done')], 'triage')
		];
		for (const b of cases) expect(celebration(b, today, open)).toBeNull();
	});

	it('Should give a small burst when there is no plan yet', () => {
		expect(celebration(batch([change(1, 'open', 'done')]), { ...today, plan: null }, open)).toBe('small');
	});
});
