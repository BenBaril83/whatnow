import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type Source } from './api';
import { cleanupText, Toasts } from './toasts.svelte';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('Toasts', () => {
	it.each([
		['cleanup', 1],
		['user', 0],
		['chat', 0],
		['triage', 0]
	] as [Source, number][])('Should toast a %s batch from the stream %i times', (source, want) => {
		const t = new Toasts();
		t.applied({ batch_id: 5, source, activity: [] });
		expect(t.items.length).toBe(want);
	});

	it('Should offer undo for a write that worked', async () => {
		const t = new Toasts();
		expect(await t.track('Done', async () => ({ batch_id: 9 }))).toBe(true);
		expect(t.items).toMatchObject([{ text: 'Done', batch: 9 }]);
	});

	it('Should show the error, with no undo, for a write that failed', async () => {
		const t = new Toasts();
		const worked = await t.track('Done', async () => {
			throw new ApiError(400, 'title is required');
		});
		expect(worked).toBe(false);
		expect(t.items).toMatchObject([{ text: 'title is required', error: true }]);
		expect(t.items[0].batch).toBeUndefined();
	});

	it('Should drop the toast when its batch is undone, from any tab', () => {
		const t = new Toasts();
		t.show('Pushed', 3);
		t.show('Dropped', 4);
		t.undone(3);
		expect(t.items.map((x) => x.batch)).toEqual([4]);
	});

	it('Should dismiss itself after about 8 seconds', () => {
		const t = new Toasts();
		t.show('Pushed', 3);
		vi.advanceTimersByTime(7999);
		expect(t.items.length).toBe(1);
		vi.advanceTimersByTime(1);
		expect(t.items.length).toBe(0);
	});
	it('Should stand still while paused, then finish only the time it had left', () => {
		const t = new Toasts();
		t.show('Pushed', 3);
		vi.advanceTimersByTime(5000);
		t.pause(t.items[0].id);
		vi.advanceTimersByTime(60_000);
		expect(t.items).toMatchObject([{ paused: true }]);

		t.resume(t.items[0].id);
		vi.advanceTimersByTime(2999);
		expect(t.items.length).toBe(1);
		vi.advanceTimersByTime(1);
		expect(t.items.length).toBe(0);
	});

	it('Should keep a nudge for 30 seconds, with its button', () => {
		const t = new Toasts();
		const run = vi.fn();
		t.nudge("Standup's done. What now?", { label: 'What now?', run });
		vi.advanceTimersByTime(29_999);
		expect(t.items).toMatchObject([{ nudge: true, action: { label: 'What now?' } }]);
		vi.advanceTimersByTime(1);
		expect(t.items.length).toBe(0);
	});

	it('Should hold a nudge that arrives in a hidden tab until the tab is seen', () => {
		const t = new Toasts();
		t.hidden(true);
		t.nudge('Standup in 10 min, find a stopping point.');
		vi.advanceTimersByTime(5 * 60_000);
		expect(t.items.length).toBe(1);
		t.hidden(false);
		vi.advanceTimersByTime(30_000);
		expect(t.items.length).toBe(0);
	});

	it('Should stop a nudge counting down when the tab is hidden, but not an undo toast', () => {
		const t = new Toasts();
		t.nudge('Standup in 10 min, find a stopping point.');
		t.show('Pushed', 3);
		vi.advanceTimersByTime(5000);
		t.hidden(true);
		vi.advanceTimersByTime(60_000);
		expect(t.items.map((x) => x.text)).toEqual(['Standup in 10 min, find a stopping point.']);
		t.hidden(false);
		vi.advanceTimersByTime(25_000);
		expect(t.items.length).toBe(0);
	});
});

describe('cleanupText', () => {
	const act = (action: string, entity: string) => ({ action, entity });
	it.each([
		['tidied one task', [act('update', 'task')], 'Tidied up your note'],
		['split into tasks', [act('update', 'task'), act('create', 'task'), act('create', 'task')], 'Split your note into 3 tasks'],
		['split into tasks and an event', [act('update', 'task'), act('create', 'task'), act('create', 'event')], 'Split your note into 2 tasks and 1 event'],
		['turned the note into events', [act('delete', 'task'), act('create', 'event'), act('create', 'event')], 'Turned your note into 2 events'],
		['blockers alone add nothing to say', [act('update', 'task'), act('add', 'blocker')], 'Tidied up your note']
	])('Should say what cleanup did when it %s', (_, activity, want) => {
		expect(cleanupText(activity)).toBe(want);
	});

	it('Should toast a cleanup batch with what it did', () => {
		const t = new Toasts();
		t.applied({ batch_id: 5, source: 'cleanup', activity: [act('update', 'task'), act('create', 'task')] });
		expect(t.items).toMatchObject([{ text: 'Split your note into 2 tasks', batch: 5 }]);
	});
});
