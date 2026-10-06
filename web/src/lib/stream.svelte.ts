import type { BatchApplied, Message, ToolUse } from './api';

export interface Handlers {
	'batch.applied': (d: BatchApplied) => void;
	'batch.undone': (d: { batch_id: number }) => void;
	'plan.updated': () => void;
	'chat.delta': (d: { text: string }) => void;
	'chat.tool': (d: ToolUse) => void;
	'chat.message': (d: Message) => void;
	'data.reset': () => void;
}

/** The parts of EventSource the stream uses, so tests can drive it. */
export interface Source {
	addEventListener(type: string, listener: (e: MessageEvent) => void): void;
	close(): void;
}

/**
 * One connection to the server's event stream. EventSource reconnects on its own; when it does,
 * onReconnect runs, since anything sent while it was down is lost.
 */
export class Stream {
	// Starts true so the page doesn't flash "reconnecting…" before the first open. A connection
	// that never opens still fires error, which sets it false.
	connected = $state(true);
	#source: Source | null = null;

	constructor(
		private handlers: Handlers,
		private onReconnect: () => void,
		private connect: () => Source = () => new EventSource('/api/v1/stream')
	) {}

	open(): void {
		const source = this.connect();
		let opened = false;
		source.addEventListener('open', () => {
			if (opened) this.onReconnect();
			opened = true;
			this.connected = true;
		});
		source.addEventListener('error', () => {
			this.connected = false;
		});
		for (const [name, handle] of Object.entries(this.handlers)) {
			source.addEventListener(name, (e) => handle(JSON.parse(e.data)));
		}
		this.#source = source;
	}

	close(): void {
		this.#source?.close();
	}
}
