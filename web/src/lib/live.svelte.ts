import { goto } from '$app/navigation';
import { api, type SettingsView } from './api';
import { chat } from './chat.svelte';
import { Stream } from './stream.svelte';
import { due, nudges, type Nudge } from './nudges';
import { morningDue } from './time';
import { toasts } from './toasts.svelte';
import { today } from './today.svelte';

/**
 * The page's live connection to the server, shared by every screen: the event stream, the
 * settings, and a clock that ticks each minute and fires the nudges due.
 */
class Live {
	view = $state<SettingsView | null>(null);
	now = $state(new Date());
	/** Nudges fire for the span since this, so each fires once and none fire for before the page loaded. */
	#lastTick = Date.now();

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
			'chat.message': (d) => chat.message(d),
			// Everything was deleted, maybe from another tab. Start over from first-run setup.
			'data.reset': () => location.assign('/')
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
		const clock = setInterval(() => this.#tick(), 60_000);
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
		this.#tick();
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

	#tick(): void {
		this.now = new Date();
		const to = this.now.getTime();
		const t = today.today;
		if (this.view && t) {
			const list = nudges(today.events, t.plan, today.tasks, this.view.settings, t.date, this.endOfDay);
			for (const n of due(list, this.#lastTick, to)) deliver(n);
		}
		this.#lastTick = to;
	}
}

/**
 * Shows a nudge as a notification when the browser allows it, else in the page. The tag makes
 * two open tabs share one notification. An event-end nudge opens What now when clicked.
 */
function deliver(n: Nudge): void {
	const open = n.kind === 'event_end' ? () => goto('/?whatnow') : undefined;
	if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
		const note = new Notification('whatnow', { body: n.text, tag: n.key });
		note.onclick = () => {
			window.focus();
			open?.();
			note.close();
		};
	} else {
		toasts.nudge(n.text, open && { label: 'What now?', run: open });
	}
}

export const live = new Live();
