import { describe, expect, it } from 'vitest';
import type { Blocker, Status, Task } from './api';
import { listTasks, type Filter } from './tasks';

const task = (id: number, over: Partial<Task> = {}): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: null,
	not_before: null,
	status: 'open' as Status,
	refs: null,
	created_at: `2026-10-0${id}T15:00:00Z`,
	updated_at: '2026-10-05T15:00:00Z',
	...over
});

const tasks = [
	task(1),
	task(2, { not_before: '2026-10-09' }),
	task(3, { status: 'done' }),
	task(4, { status: 'dropped' }),
	task(5),
	task(6, { not_before: '2026-10-05' }) // due back today: not future
];
// 5 waits on 1, which is open; 6 waited on 3, which is done.
const blockers: Blocker[] = [
	{ task_id: 5, blocked_by_id: 1 },
	{ task_id: 6, blocked_by_id: 3 }
];

describe('listTasks', () => {
	it.each([
		['open', [6, 5, 2, 1]],
		['future', [2]],
		['blocked', [5]],
		['done', [3]],
		['dropped', [4]],
		['all', [6, 5, 4, 3, 2, 1]]
	] as [Filter, number[]][])('Should show the right tasks for %s', (filter, want) => {
		expect(listTasks(tasks, blockers, filter, '2026-10-05').map((t) => t.id)).toEqual(want);
	});

	it('Should sort by due date, earliest first and undated last, then newest first', () => {
		const got = listTasks(
			[task(1), task(2, { due_on: '2026-10-20' }), task(3, { due_on: '2026-10-07' }), task(4)],
			[],
			'open',
			'2026-10-05'
		);
		expect(got.map((t) => t.id)).toEqual([3, 2, 4, 1]);
	});
});
