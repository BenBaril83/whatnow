import { api, ApiError, type Message, type ToolUse } from './api';

/** The reply being written, built from deltas and tool chips until its stored message lands. */
export interface Draft {
	text: string;
	tools: ToolUse[];
}

export class Chat {
	messages = $state<Message[]>([]);
	draft = $state<Draft | null>(null);
	busy = $state(false);
	/** A gentle line under the input, such as when another tab is mid-reply. */
	notice = $state('');
	/** Reply batches undone, so their Undo buttons read "undone". */
	undone = $state<number[]>([]);
	open = $state(true);
	/** A reply landed while the dock was collapsed. */
	unread = $state(false);

	/** Stored history is authoritative, so a draft whose reply was lost to a dropped stream goes. */
	async load(): Promise<void> {
		this.messages = await api.chat();
		this.draft = null;
	}

	delta(text: string): void {
		this.draft ??= { text: '', tools: [] };
		this.draft.text += text;
	}

	tool(use: ToolUse): void {
		this.draft ??= { text: '', tools: [] };
		this.draft.tools.push(use);
	}

	/** A stored message is authoritative: it replaces the draft it finishes. */
	message(m: Message): void {
		const i = this.messages.findIndex((x) => x.id === m.id);
		if (i >= 0) this.messages[i] = m;
		else this.messages.push(m);
		if (m.role === 'assistant') {
			this.draft = null;
			if (!this.open) this.unread = true;
		}
	}

	markUndone(batch: number): void {
		this.undone.push(batch);
	}

	toggle(): void {
		this.open = !this.open;
		if (this.open) this.unread = false;
	}

	/** Sends a message. Returns false when it wasn't taken, so the input keeps the text. */
	async send(text: string): Promise<boolean> {
		this.busy = true;
		this.notice = '';
		try {
			this.message(await api.send(text));
			return true;
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				this.notice = 'One sec, still answering.';
			} else {
				this.notice = err instanceof Error ? err.message : String(err);
			}
			return false;
		} finally {
			this.busy = false;
		}
	}
}

export const chat = new Chat();
