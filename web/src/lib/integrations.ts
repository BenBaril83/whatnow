import type { Field, Integration, IntegrationUpdate, PullResult } from './api';

/**
 * A settings form's copy of an integration's fields, in the shape saving sends. Secrets start
 * blank, so saving without typing one keeps it.
 */
export function formFrom(i: Integration): IntegrationUpdate {
	const values: Record<string, string> = {};
	const lists: IntegrationUpdate['lists'] = {};
	for (const f of i.fields) {
		if (f.kind === 'text') values[f.key] = i.values[f.key] ?? '';
		if (f.kind === 'secret') values[f.key] = '';
		if (f.kind === 'list') {
			lists[f.key] = (i.lists[f.key] ?? []).map((row) => ({ id: row.id, values: blankSecrets(f, row.values) }));
		}
	}
	return { values, lists };
}

/** A new row for a list field: every column blank and no id, which the server gives it. */
export function newRow(list: Field): { id: string; values: Record<string, string> } {
	return { id: '', values: blankSecrets(list, {}) };
}

function blankSecrets(list: Field, saved: Record<string, string>): Record<string, string> {
	const values: Record<string, string> = {};
	for (const c of list.fields ?? []) values[c.key] = c.kind === 'secret' ? '' : (saved[c.key] ?? '');
	return values;
}

/** Whether an integration can pull: it's on, and its required fields are filled. */
export function isOn(i: Integration): boolean {
	return i.enabled && i.ready;
}

/** The result without one event, after it was removed or its series skipped. */
export function withoutEvent(result: PullResult, id: number): PullResult {
	return {
		...result,
		sources: result.sources.map((s) => ({ ...s, events: s.events.filter((e) => e.id !== id) }))
	};
}
