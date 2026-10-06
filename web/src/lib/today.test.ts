import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type Today } from './api';
import { TodayState } from './today.svelte';

const day: Today = { date: '2026-10-05', day: null, plan: null, unplanned: [] };

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

describe('TodayState', () => {
	it("Should fetch today's events for the server's date", async () => {
		vi.spyOn(api, 'today').mockResolvedValue(day);
		const events = vi.spyOn(api, 'events').mockResolvedValue([]);
		vi.spyOn(api, 'tasks').mockResolvedValue([]);
		vi.spyOn(api, 'blockers').mockResolvedValue([]);
		await new TodayState().refresh();
		expect(events).toHaveBeenCalledWith('2026-10-05');
	});

	it('Should refetch once for a burst of changes', async () => {
		const fetched = vi.spyOn(api, 'today').mockResolvedValue(day);
		vi.spyOn(api, 'events').mockResolvedValue([]);
		vi.spyOn(api, 'tasks').mockResolvedValue([]);
		vi.spyOn(api, 'blockers').mockResolvedValue([]);
		const t = new TodayState();
		t.changed();
		t.changed();
		t.changed();
		await vi.advanceTimersByTimeAsync(100);
		expect(fetched).toHaveBeenCalledTimes(1);
	});
	it('Should refresh the blockers with everything else', async () => {
		vi.spyOn(api, 'today').mockResolvedValue(day);
		vi.spyOn(api, 'events').mockResolvedValue([]);
		vi.spyOn(api, 'tasks').mockResolvedValue([]);
		vi.spyOn(api, 'blockers').mockResolvedValue([{ task_id: 2, blocked_by_id: 1 }]);
		const t = new TodayState();
		await t.refresh();
		expect(t.blockers).toEqual([{ task_id: 2, blocked_by_id: 1 }]);
	});
});
