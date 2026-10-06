import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type Source } from './api';
import { Toasts } from './toasts.svelte';

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
		t.applied({ batch_id: 5, source });
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
});
