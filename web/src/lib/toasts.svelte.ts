import { api, type BatchApplied, type Mutation } from './api';

export interface Toast {
	id: number;
	text: string;
	/** The batch Undo reverts. Absent on an error. */
	batch?: number;
	error?: boolean;
}

const lifetime = 8000;

export class Toasts {
	items = $state<Toast[]>([]);
	#next = 1;

	#add(toast: Omit<Toast, 'id'>): void {
		const id = this.#next++;
		this.items.push({ id, ...toast });
		setTimeout(() => this.dismiss(id), lifetime);
	}

	show(text: string, batch: number): void {
		this.#add({ text, batch });
	}

	error(err: unknown): void {
		this.#add({ text: err instanceof Error ? err.message : String(err), error: true });
	}

	dismiss(id: number): void {
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
		this.items = this.items.filter((t) => t.batch !== batch);
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
