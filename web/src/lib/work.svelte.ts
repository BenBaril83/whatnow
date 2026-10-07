/** A slow model call the server announces with work.started and work.finished. */
export type WorkKind = 'replan' | 'cleanup';

/** What the server is working on right now, so screens can say so. */
export class Work {
	#running = $state<Record<WorkKind, number>>({ replan: 0, cleanup: 0 });

	busy(kind: WorkKind): boolean {
		return this.#running[kind] > 0;
	}

	started(d: { kind: WorkKind }): void {
		this.#running[d.kind]++;
	}

	finished(d: { kind: WorkKind }): void {
		this.#running[d.kind] = Math.max(0, this.#running[d.kind] - 1);
	}

	reset(): void {
		this.#running = { replan: 0, cleanup: 0 };
	}
}

export const work = new Work();
