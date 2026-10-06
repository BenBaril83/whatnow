import { api, type CalendarEvent, type Task, type Today } from './api';
import { toasts } from './toasts.svelte';

const debounce = 100;

/** Today's state, refetched whole whenever the server says something changed. */
export class TodayState {
	today = $state<Today | null>(null);
	events = $state<CalendarEvent[]>([]);
	tasks = $state<Task[]>([]);
	#timer: ReturnType<typeof setTimeout> | undefined;

	async refresh(): Promise<void> {
		const today = await api.today();
		const [events, tasks] = await Promise.all([api.events(today.date), api.tasks()]);
		this.today = today;
		this.events = events;
		this.tasks = tasks;
	}

	/** Refreshes once a burst of changes settles. */
	changed(): void {
		clearTimeout(this.#timer);
		this.#timer = setTimeout(() => this.refresh().catch((err) => toasts.error(err)), debounce);
	}
}

export const today = new TodayState();
