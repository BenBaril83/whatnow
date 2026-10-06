import { api, type BatchApplied, type Mutation } from './api';

export interface Toast {
	id: number;
	text: string;
	/** The batch Undo reverts. Absent on an error. */
	batch?: number;
	error?: boolean;
	/** Set while hovered or focused: the countdown and its fill stand still. */
	paused?: boolean;
}

/** How long a toast stays, in ms. Its fill sweeps across in the same time. */
export const lifetime = 8000;

interface Timer {
	handle: ReturnType<typeof setTimeout>;
	endsAt: number;
	left: number;
}

export class Toasts {
	items = $state<Toast[]>([]);
	#next = 1;
	#timers = new Map<number, Timer>();

	#add(toast: Omit<Toast, 'id'>): void {
		const id = this.#next++;
		this.items.push({ id, ...toast });
		this.#run(id, lifetime);
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

	show(text: string, batch: number): void {
		this.#add({ text, batch });
	}

	error(err: unknown): void {
		this.#add({ text: err instanceof Error ? err.message : String(err), error: true });
	}

	dismiss(id: number): void {
		clearTimeout(this.#timers.get(id)?.handle);
		this.#timers.delete(id);
		this.items = this.items.filter((t) => t.id !== id);
	}

	/** Runs a write and offers its undo, or shows why it failed. Returns whether it worked. */
	async track(text: string, write: () => Promise<Mutation>): Promise<boolean> {
		try {
			this.show(text, (await write()).batch_id);
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
		if (b.source === 'cleanup') this.show("Here's what I did", b.batch_id);
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

export const toasts = new Toasts();
