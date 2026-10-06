import { api, type Blocker, type CalendarEvent, type Task, type Today } from './api';
import { toasts } from './toasts.svelte';

const debounce = 100;

/** Today's state, refetched whole whenever the server says something changed. */
export class TodayState {
	today = $state<Today | null>(null);
	events = $state<CalendarEvent[]>([]);
	tasks = $state<Task[]>([]);
	blockers = $state<Blocker[]>([]);
	#timer: ReturnType<typeof setTimeout> | undefined;

	async refresh(): Promise<void> {
		const today = await api.today();
		const [events, tasks, blockers] = await Promise.all([
			api.events(today.date),
			api.tasks(),
			api.blockers()
		]);
		this.today = today;
		this.events = events;
		this.tasks = tasks;
		this.blockers = blockers;
	}

	/** Refreshes once a burst of changes settles. */
	changed(): void {
		clearTimeout(this.#timer);
		this.#timer = setTimeout(() => this.refresh().catch((err) => toasts.error(err)), debounce);
	}
}

export const today = new TodayState();
