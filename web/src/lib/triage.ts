import type { Action, Review, Triage } from './api';

/** One task's decision. Until is the push date, kept even while another action is picked. */
export interface Choice {
	action: Action;
	until: string;
}

export type Choices = Record<number, Choice>;

/**
 * The choices for a review's unfinished tasks: what the user already picked for a task still on
 * it, else the model's suggestion, else roll over. A push date defaults to tomorrow.
 */
export function choicesFor(review: Review, tomorrow: string, kept: Choices = {}): Choices {
	const suggested = new Map(review.suggestions.map((s) => [s.task_id, s]));
	const out: Choices = {};
	for (const task of review.unfinished) {
		const s = suggested.get(task.id);
		out[task.id] = kept[task.id] ?? {
			action: s?.action ?? 'roll_over',
			until: s?.action === 'push' && s.until ? s.until : tomorrow
		};
	}
	return out;
}

/** The triage to send. An empty push date leaves until out, which the server reads as tomorrow. */
export function triagePayload(choices: Choices): Triage[] {
	return Object.entries(choices).map(([id, c]) => ({
		task_id: Number(id),
		action: c.action,
		until: c.action === 'push' && c.until ? c.until : null
	}));
}
