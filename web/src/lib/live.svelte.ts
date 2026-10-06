import { goto } from '$app/navigation';
import { api, type SettingsView } from './api';
import { chat } from './chat.svelte';
import { Stream } from './stream.svelte';
import { morningDue } from './time';
import { toasts } from './toasts.svelte';
import { today } from './today.svelte';

/**
 * The page's live connection to the server, shared by every screen: the event stream, the
 * settings, and a clock that ticks each minute.
 */
class Live {
	view = $state<SettingsView | null>(null);
	now = $state(new Date());

	#stream = new Stream(
		{
			'batch.applied': (d) => {
				today.changed();
				toasts.applied(d);
			},
			'batch.undone': (d) => {
				today.changed();
				toasts.undone(d.batch_id);
				chat.markUndone(d.batch_id);
			},
			'plan.updated': () => today.changed(),
			'chat.delta': (d) => chat.delta(d.text),
			'chat.tool': (d) => chat.tool(d),
			'chat.message': (d) => chat.message(d)
		},
		() => {
			today.changed();
			chat.load().catch((err) => toasts.error(err));
		}
	);

	get connected(): boolean {
		return this.#stream.connected;
	}

	/** When today's working day ends, from the settings. */
	get endOfDay(): Date {
		const date = today.today?.date ?? '';
		return new Date(`${date}T${this.view?.settings.end_of_day ?? '17:30'}:00`);
	}

	/** Connects once for the life of the page. Returns the disconnect. */
	start(): () => void {
		Promise.all([today.refresh(), chat.load(), api.settings().then((v) => (this.view = v))]).catch(
			(err) => toasts.error(err)
		);
		this.#stream.open();
		const clock = setInterval(() => (this.now = new Date()), 60_000);
		const focus = () => this.#caughtUp();
		window.addEventListener('focus', focus);
		return () => {
			clearInterval(clock);
			window.removeEventListener('focus', focus);
			this.#stream.close();
		};
	}

	// A tab left open overnight still shows yesterday. Coming back to it catches up, and starts the
	// morning when the new day has no plan yet.
	async #caughtUp(): Promise<void> {
		this.now = new Date();
		const seen = today.today?.date;
		try {
			await today.refresh();
		} catch (err) {
			toasts.error(err);
			return;
		}
		const t = today.today;
		if (seen && t && morningDue(seen, t.date, t.plan !== null)) goto('/morning');
	}
}

export const live = new Live();
