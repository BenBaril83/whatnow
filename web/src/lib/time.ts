import type { CalendarEvent } from './api';

const minute = 60_000;

/** An event with no end counts as 30 minutes, the same as the server's facts. */
export function eventEnd(e: CalendarEvent): number {
	return e.ends_at ? Date.parse(e.ends_at) : Date.parse(e.starts_at) + 30 * minute;
}

/** The local YYYY-MM-DD of a moment. */
export function localDate(d: Date): string {
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function addDays(date: string, days: number): string {
	const d = new Date(`${date}T12:00:00`); // noon keeps a DST shift from changing the date
	d.setDate(d.getDate() + days);
	return localDate(d);
}

function span(ms: number): string {
	const mins = Math.ceil(ms / minute);
	if (mins < 60) return `${mins}m`;
	const h = Math.floor(mins / 60);
	const m = mins % 60;
	return m ? `${h}h ${m}m` : `${h}h`;
}

/** "Standup now" while an event runs, else "Standup in 24m" for the next one, else null. */
export function countdown(events: CalendarEvent[], now: Date): string | null {
	const t = now.getTime();
	const current = events.find((e) => Date.parse(e.starts_at) <= t && t < eventEnd(e));
	if (current) return `${current.title} now`;
	const next = events
		.filter((e) => Date.parse(e.starts_at) > t)
		.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))[0];
	return next ? `${next.title} in ${span(Date.parse(next.starts_at) - t)}` : null;
}

export interface StripItem {
	event: CalendarEvent;
	/** Percent of the strip's width. */
	left: number;
	width: number;
}

export interface StripTick {
	left: number;
	/** Like "9a" or "12p". */
	label: string;
}

export interface Strip {
	items: StripItem[];
	now: number;
	ticks: StripTick[];
	endOfDay: number;
}

const hour = 60 * minute;

function floorHour(ms: number): number {
	const d = new Date(ms);
	d.setMinutes(0, 0, 0);
	return d.getTime();
}

function hourLabel(h: number): string {
	return `${h % 12 || 12}${h < 12 ? 'a' : 'p'}`;
}

/**
 * Places events on a strip that holds still while the now line crosses it. It runs from the
 * earliest of when the day started, the first event, and now, to the end of day, rounded out to
 * whole hours. It stretches past the end of day to keep now and every event on it. A start
 * anchored at now would pin the now line to the left edge.
 *
 * Ticks mark every third clock hour, or every hour on a strip of 6 hours or less.
 */
export function strip(events: CalendarEvent[], now: Date, endOfDay: Date, startedAt: string | null): Strip {
	const t = now.getTime();
	const starts = events.map((e) => Date.parse(e.starts_at));
	const start = floorHour(Math.min(t, ...starts, ...(startedAt ? [Date.parse(startedAt)] : [])));
	const end = floorHour(Math.max(endOfDay.getTime(), t, ...events.map(eventEnd))) + hour;
	const pct = (ms: number) => ((ms - start) / (end - start)) * 100;
	const step = end - start <= 6 * hour ? 1 : 3;
	const ticks: StripTick[] = [];
	for (let h = start + hour; h < end; h += hour) {
		const clock = new Date(h).getHours();
		if (clock % step === 0) ticks.push({ left: pct(h), label: hourLabel(clock) });
	}
	return {
		items: events.map((event) => ({
			event,
			left: pct(Date.parse(event.starts_at)),
			width: pct(eventEnd(event)) - pct(Date.parse(event.starts_at))
		})),
		now: pct(t),
		ticks,
		endOfDay: pct(endOfDay.getTime())
	};
}

/** Today's events that have ended. */
export function survived(events: CalendarEvent[], now: Date): number {
	return events.filter((e) => eventEnd(e) <= now.getTime()).length;
}

/** Wrap-up is offered from an hour before the end of day. */
export function wrapDue(now: Date, endOfDay: Date): boolean {
	return now.getTime() >= endOfDay.getTime() - 60 * minute;
}

/**
 * Whether a tab that last saw `seen` should start the morning now: the date has moved on and the
 * new day has no plan yet. A tab left open overnight otherwise keeps showing yesterday.
 */
export function morningDue(seen: string, date: string, planned: boolean): boolean {
	return date !== seen && !planned;
}
