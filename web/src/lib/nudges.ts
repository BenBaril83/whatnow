import type { CalendarEvent, Plan, Settings, Task } from './api';

const minute = 60_000;

export type NudgeKind = 'before_event' | 'event_end' | 'late_day';

export interface Nudge {
	/** The same nudge always has the same key, so two tabs can share one notification. */
	key: string;
	/** When it's due, in ms since the epoch. */
	at: number;
	kind: NudgeKind;
	text: string;
}

/**
 * Today's nudges from its events, plan, and tasks, leaving out the kinds turned off in settings.
 * `date` is today's YYYY-MM-DD and `endOfDay` the moment the working day ends.
 */
export function nudges(
	events: CalendarEvent[],
	plan: Plan | null,
	tasks: Task[],
	settings: Settings,
	date: string,
	endOfDay: Date
): Nudge[] {
	const lead = settings.alert_lead_minutes;
	const out: Nudge[] = [];
	for (const e of events) {
		const start = Date.parse(e.starts_at);
		const before = start - lead * minute;
		// An event added inside its own lead time is news already; a nudge would just repeat it.
		if (before >= Date.parse(e.created_at)) {
			out.push({
				key: `before_event:${e.id}:${start}`,
				at: before,
				kind: 'before_event',
				text: `${e.title} in ${lead} min, find a stopping point.`
			});
		}
		if (e.ends_at) {
			const end = Date.parse(e.ends_at);
			out.push({ key: `event_end:${e.id}:${end}`, at: end, kind: 'event_end', text: `${e.title}'s done. What now?` });
		}
	}
	const stalled = stalledMust(plan, tasks, date);
	if (stalled) {
		out.push({
			key: `late_day:${date}`,
			at: endOfDay.getTime() - 120 * minute,
			kind: 'late_day',
			text: `${stalled.title} is due today and hasn't moved yet. Still time.`
		});
	}
	return out.filter((n) => settings.nudges[n.kind]);
}

// The first open must due today that nothing has touched today. Every edit, push, and tick moves
// updated_at, which stands in for "no activity today".
function stalledMust(plan: Plan | null, tasks: Task[], date: string): Task | undefined {
	const dayStart = new Date(`${date}T00:00:00`).getTime();
	const musts = new Set(plan?.entries.filter((e) => e.tier === 'must').map((e) => e.task_id));
	return tasks.find(
		(t) => musts.has(t.id) && t.status === 'open' && t.due_on === date && Date.parse(t.updated_at) < dayStart
	);
}

/**
 * The nudges due after `from` and by `to`. Each tick asks for the span since the last one, so a
 * nudge fires once, a reload never fires old ones, and a long gap in a throttled tab still fires.
 */
export function due(list: Nudge[], from: number, to: number): Nudge[] {
	return list.filter((n) => from < n.at && n.at <= to);
}
