import type { BatchApplied, Task, Today } from './api';
import { tiers } from './plan';

/** How big a cheer a batch earns: the finale is for the last open task in today's plan. */
export type Celebration = 'small' | 'must' | 'finale';

/**
 * The cheer for the tasks a batch finished, or null if it finished none. Morning triage doesn't
 * count: marking yesterday's leftovers done isn't today's win.
 */
export function celebration(b: BatchApplied, today: Today | null, tasks: Task[]): Celebration | null {
	if (b.source === 'triage') return null;
	const done = new Set(
		b.activity
			.filter((a) => a.entity === 'task' && a.action === 'status' && a.after?.status === 'done' && a.before?.status !== 'done')
			.map((a) => a.entity_id)
	);
	if (done.size === 0) return null;
	if (!today?.plan) return 'small';

	const before = tiers(today, tasks);
	const after = tiers(today, tasks.map((t) => (done.has(t.id) ? { ...t, status: 'done' as const } : t)));
	const left = (t: typeof before) => t.must.length + t.should.length + t.could.length;
	if (left(before) > 0 && left(after) === 0) return 'finale';
	return before.must.some((c) => done.has(c.task.id)) ? 'must' : 'small';
}
