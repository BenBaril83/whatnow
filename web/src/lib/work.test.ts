import { describe, expect, it } from 'vitest';
import { Work } from './work.svelte';

describe('Work', () => {
	it('Should be busy with a kind from started until finished', () => {
		const w = new Work();
		w.started({ kind: 'replan' });
		expect(w.busy('replan')).toBe(true);
		expect(w.busy('cleanup')).toBe(false);
		w.finished({ kind: 'replan' });
		expect(w.busy('replan')).toBe(false);
	});

	it('Should stay busy until every overlapping run of a kind finishes', () => {
		const w = new Work();
		w.started({ kind: 'cleanup' });
		w.started({ kind: 'cleanup' });
		w.finished({ kind: 'cleanup' });
		expect(w.busy('cleanup')).toBe(true);
		w.finished({ kind: 'cleanup' });
		expect(w.busy('cleanup')).toBe(false);
	});

	it('Should ignore a finish it never saw start, as after a reconnect', () => {
		const w = new Work();
		w.finished({ kind: 'replan' });
		w.started({ kind: 'replan' });
		expect(w.busy('replan')).toBe(true);
	});

	it('Should forget everything on reset, since a finish sent while disconnected is lost', () => {
		const w = new Work();
		w.started({ kind: 'replan' });
		w.reset();
		expect(w.busy('replan')).toBe(false);
	});
});
