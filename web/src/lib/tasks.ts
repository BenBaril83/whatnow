import type { Blocker, Task } from './api';

export type Filter = 'open' | 'future' | 'blocked' | 'done' | 'dropped' | 'all';

export const filters: { id: Filter; label: string }[] = [
	{ id: 'open', label: 'Open' },
	{ id: 'future', label: 'Future' },
	{ id: 'blocked', label: 'Blocked' },
	{ id: 'done', label: 'Done' },
	{ id: 'dropped', label: 'Dropped' },
	{ id: 'all', label: 'All' }
];

/** The tasks a filter shows, sorted by due date (undated last), then newest first. */
export function listTasks(tasks: Task[], blockers: Blocker[], filter: Filter, today: string): Task[] {
	const open = new Set(tasks.filter((t) => t.status === 'open').map((t) => t.id));
	const blocked = new Set(blockers.filter((b) => open.has(b.blocked_by_id)).map((b) => b.task_id));
	const keep: Record<Filter, (t: Task) => boolean> = {
		open: (t) => t.status === 'open',
		future: (t) => t.status === 'open' && (t.not_before ?? '') > today,
		blocked: (t) => t.status === 'open' && blocked.has(t.id),
		done: (t) => t.status === 'done',
		dropped: (t) => t.status === 'dropped',
		all: () => true
	};
	return tasks.filter(keep[filter]).sort((a, b) => {
		if (a.due_on !== b.due_on) {
			if (!a.due_on) return 1;
			if (!b.due_on) return -1;
			return a.due_on < b.due_on ? -1 : 1;
		}
		return b.created_at.localeCompare(a.created_at);
	});
}
