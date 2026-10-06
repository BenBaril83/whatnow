import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, type Message } from './api';
import { Chat } from './chat.svelte';

afterEach(() => vi.restoreAllMocks());

const reply = (id: number, text: string, over: Partial<Message> = {}): Message => ({
	id,
	role: 'assistant',
	text,
	tools: null,
	batch_id: 0,
	error: '',
	...over
});

describe('Chat', () => {
	it('Should build a draft from deltas and chips, then swap in the stored reply', () => {
		const c = new Chat();
		c.tool({ tool: 'complete_task', label: 'Done: taxes', error: '' });
		c.delta('Nice, ');
		c.delta('that one is off your plate.');
		expect(c.draft).toEqual({
			text: 'Nice, that one is off your plate.',
			tools: [{ tool: 'complete_task', label: 'Done: taxes', error: '' }]
		});

		c.message(reply(2, 'Nice, that one is off your plate!', { batch_id: 7 }));
		expect(c.draft).toBeNull();
		expect(c.messages.map((m) => m.text)).toEqual(['Nice, that one is off your plate!']);
	});

	it('Should take a stored reply that arrives with no deltas before it', () => {
		const c = new Chat();
		c.message(reply(2, 'Short and sweet.'));
		expect(c.messages).toHaveLength(1);
		expect(c.draft).toBeNull();
	});

	it('Should keep a user message from ending the draft, and not list a message twice', () => {
		const c = new Chat();
		c.delta('Thinking');
		c.message({ ...reply(1, 'hi'), role: 'user' });
		expect(c.draft?.text).toBe('Thinking');
		c.message(reply(2, 'Hello'));
		c.message(reply(2, 'Hello')); // the stream and the response both carry the reply
		expect(c.messages.map((m) => m.id)).toEqual([1, 2]);
	});

	it('Should drop a half-written draft when history reloads after a reconnect', async () => {
		vi.spyOn(api, 'chat').mockResolvedValue([reply(2, 'The whole reply.')]);
		const c = new Chat();
		c.delta('The whole'); // the stream dropped before the stored reply arrived
		await c.load();
		expect(c.draft).toBeNull();
		expect(c.messages.map((m) => m.text)).toEqual(['The whole reply.']);
	});

	it('Should mark a reply unread only while the dock is collapsed', () => {
		const c = new Chat();
		c.message(reply(1, 'open dock'));
		expect(c.unread).toBe(false);
		c.toggle();
		c.message(reply(2, 'collapsed dock'));
		expect(c.unread).toBe(true);
		c.toggle();
		expect(c.unread).toBe(false);
	});

	it('Should keep the text and say so when another tab is mid-reply', async () => {
		vi.spyOn(api, 'send').mockRejectedValue(new ApiError(409, 'a reply is still being written'));
		const c = new Chat();
		expect(await c.send('hello')).toBe(false);
		expect(c.notice).toBe('One sec, still answering.');
		expect(c.busy).toBe(false);
	});

	it('Should add the reply from the response, in case the stream missed it', async () => {
		vi.spyOn(api, 'send').mockResolvedValue(reply(3, 'Done.'));
		const c = new Chat();
		expect(await c.send('finish taxes')).toBe(true);
		expect(c.messages.map((m) => m.text)).toEqual(['Done.']);
	});
});
