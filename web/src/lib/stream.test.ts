import { describe, expect, it, vi } from 'vitest';
import { Stream, type Handlers, type Source } from './stream.svelte';

class FakeSource implements Source {
	listeners = new Map<string, (e: MessageEvent) => void>();
	addEventListener(type: string, listener: (e: MessageEvent) => void): void {
		this.listeners.set(type, listener);
	}
	emit(type: string, data: unknown = null): void {
		this.listeners.get(type)?.(new MessageEvent(type, { data: JSON.stringify(data) }));
	}
	close(): void {}
}

function handlers(): Handlers {
	return {
		'batch.applied': vi.fn(),
		'batch.undone': vi.fn(),
		'plan.updated': vi.fn(),
		'chat.delta': vi.fn(),
		'chat.tool': vi.fn(),
		'chat.message': vi.fn()
	};
}

describe('Stream', () => {
	it('Should route each event to its handler by name, parsed', () => {
		const fake = new FakeSource();
		const h = handlers();
		new Stream(h, vi.fn(), () => fake).open();

		fake.emit('chat.delta', { text: 'hi' });
		fake.emit('batch.applied', { batch_id: 4, source: 'cleanup' });

		expect(h['chat.delta']).toHaveBeenCalledWith({ text: 'hi' });
		expect(h['batch.applied']).toHaveBeenCalledWith({ batch_id: 4, source: 'cleanup' });
		expect(h['chat.message']).not.toHaveBeenCalled();
	});

	it('Should catch up after a reconnect, but not on the first connect', () => {
		const fake = new FakeSource();
		const caughtUp = vi.fn();
		const s = new Stream(handlers(), caughtUp, () => fake);
		s.open();

		fake.emit('open');
		expect(caughtUp).not.toHaveBeenCalled();
		expect(s.connected).toBe(true);

		fake.emit('error');
		expect(s.connected).toBe(false);
		fake.emit('open');
		expect(caughtUp).toHaveBeenCalledTimes(1);
	});
});
