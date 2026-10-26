import { api, type BatchApplied, type Mutation } from './api';
import { session, sessionEnded } from './session.svelte';

export interface Toast {
	id: number;
	text: string;
	/** The batch Undo reverts. Absent on an error. */
	batch?: number;
	error?: boolean;
	success?: boolean;
	/** Set while hovered or focused: the countdown and its fill stand still. */
	paused?: boolean;
	/** How long it stays, in ms. Its fill sweeps across in the same time. */
	life: number;
	nudge?: boolean;
	/** A nudge's button, such as What now? for an event that just ended. */
	action?: { label: string; run: () => void };
}

const lifetime = 5000;
/** A nudge stays longer: it arrives unasked, often while you're looking elsewhere. */
const nudgeLifetime = 30_000;

interface Timer {
	handle: ReturnType<typeof setTimeout>;
	endsAt: number;
	left: number;
}

export class Toasts {
	items = $state<Toast[]>([]);
	#next = 1;
	#timers = new Map<number, Timer>();
	#hidden = false;

	#add(toast: Omit<Toast, 'id' | 'life'>, life = lifetime): number {
		const id = this.#next++;
		this.items.push({ id, life, ...toast });
		this.#run(id, life);
		return id;
	}

	#run(id: number, ms: number): void {
		this.#timers.set(id, { handle: setTimeout(() => this.dismiss(id), ms), endsAt: Date.now() + ms, left: ms });
	}

	/** Stops a toast's countdown where it is, so reaching Undo is never a race. */
	pause(id: number): void {
		const timer = this.#timers.get(id);
		const toast = this.items.find((t) => t.id === id);
		if (!timer || !toast || toast.paused) return;
		clearTimeout(timer.handle);
		timer.left = Math.max(0, timer.endsAt - Date.now());
		toast.paused = true;
	}

	/** Restarts a paused countdown for only the time it had left. */
	resume(id: number): void {
		const timer = this.#timers.get(id);
		const toast = this.items.find((t) => t.id === id);
		if (!timer || !toast?.paused) return;
		toast.paused = false;
		this.#run(id, timer.left);
	}

	show(text: string, batch?: number): void {
		this.#add({ text, batch });
	}

	/** A write that worked, shown as a win rather than a plain note. */
	success(text: string): void {
		this.#add({ text, success: true });
	}

	error(err: unknown): void {
		if (sessionEnded(session.value, err)) {
			location.assign('/');
			return;
		}
		this.#add({ text: err instanceof Error ? err.message : String(err), error: true });
	}

	/** A nudge shown in the page, for when the browser won't show notifications. */
	nudge(text: string, action?: Toast['action']): void {
		const id = this.#add({ text, nudge: true, action }, nudgeLifetime);
		if (this.#hidden) this.pause(id);
	}

	/** Nudges wait while the tab is hidden, so one that fires meanwhile is there when you look. */
	hidden(hidden: boolean): void {
		this.#hidden = hidden;
		for (const t of this.items.filter((t) => t.nudge)) {
			if (hidden) this.pause(t.id);
			else this.resume(t.id);
		}
	}

	dismiss(id: number): void {
		clearTimeout(this.#timers.get(id)?.handle);
		this.#timers.delete(id);
		this.items = this.items.filter((t) => t.id !== id);
	}

	/**
	 * Runs a write and offers its undo, or shows why it failed. Returns whether it worked. A write
	 * with batch 0, such as a skip, changed nothing undo can revert, so it gets no Undo.
	 */
	async track(text: string, write: () => Promise<Mutation>): Promise<boolean> {
		try {
			this.show(text, (await write()).batch_id || undefined);
			return true;
		} catch (err) {
			this.error(err);
			return false;
		}
	}

	/**
	 * Only cleanup's batches get a toast from the stream. A user write gets its toast from its own
	 * response, a chat batch's undo is on its reply, and triage belongs to the morning and wrap-up
	 * flows.
	 */
	applied(b: BatchApplied): void {
		if (b.source === 'cleanup') this.show(cleanupText(b.activity), b.batch_id);
	}

	undone(batch: number): void {
		for (const t of this.items.filter((t) => t.batch === batch)) this.dismiss(t.id);
	}

	async undo(toast: Toast): Promise<void> {
		if (toast.batch === undefined) return;
		try {
			await api.undo(toast.batch);
		} catch (err) {
			this.error(err);
		}
	}
}

const count = (n: number, one: string) => `${n} ${n === 1 ? one : `${one}s`}`;

/** What a cleanup batch did to a captured note, in a few words. Blockers show on the cards instead. */
export function cleanupText(activity: BatchApplied['activity']): string {
	const n = (action: string, entity: string) =>
		activity.filter((a) => a.action === action && a.entity === entity).length;
	const tasks = n('create', 'task') + n('update', 'task');
	const events = n('create', 'event');
	if (tasks === 0 && events > 0) return `Turned your note into ${count(events, 'event')}`;
	if (tasks <= 1 && events === 0) return 'Tidied up your note';
	return `Split your note into ${count(tasks, 'task')}${events ? ` and ${count(events, 'event')}` : ''}`;
}

export const toasts = new Toasts();
