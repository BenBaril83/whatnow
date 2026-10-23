import { describe, expect, it } from 'vitest';
import type { Field, Integration, PullResult } from './api';
import { formFrom, isOn, newRow, picked, sentLabel, withoutEvent, withoutTicket } from './integrations';

const calendars: Field = {
	key: 'calendars',
	label: 'Calendars',
	kind: 'list',
	required: true,
	fields: [
		{ key: 'label', label: 'Name', kind: 'text', required: true },
		{ key: 'url', label: 'Secret address', kind: 'secret', required: true }
	]
};

const gcal: Integration = {
	id: 'gcal',
	name: 'Google Calendar',
	pulls: 'events',
	fields: [{ key: 'team', label: 'Team', kind: 'text', required: false }, { key: 'token', label: 'Token', kind: 'secret', required: false }, calendars],
	enabled: true,
	ready: true,
	values: { team: 'Platform' },
	secrets: { token: true },
	lists: { calendars: [{ id: 'r1', values: { label: 'Work' }, secrets: { url: true } }] },
	skipped: [],
	via: 'direct',
	sent_at: null
};

describe('formFrom', () => {
	it('Should copy text and leave every secret blank, so saving keeps them', () => {
		expect(formFrom(gcal)).toEqual({
			values: { team: 'Platform', token: '' },
			lists: { calendars: [{ id: 'r1', values: { label: 'Work', url: '' } }] }
		});
	});

	it('Should start a never-saved integration empty', () => {
		expect(formFrom({ ...gcal, values: {}, lists: {} })).toEqual({
			values: { team: '', token: '' },
			lists: { calendars: [] }
		});
	});
});

describe('newRow', () => {
	it('Should have no id and a blank value for every column', () => {
		expect(newRow(calendars)).toEqual({ id: '', values: { label: '', url: '' } });
	});
});

describe('isOn', () => {
	it.each([
		[true, true, true],
		[true, false, false],
		[false, true, false]
	])('Should be on only when enabled (%s) and ready (%s)', (enabled, ready, want) => {
		expect(isOn({ ...gcal, enabled, ready })).toBe(want);
	});
});

describe('withoutEvent', () => {
	it('Should drop the event from whichever source has it, keeping the rest', () => {
		const ev = (id: number) => ({ id, title: `e${id}`, starts_at: '', ends_at: null, series: '' });
		const result: PullResult = {
			batch_id: 1,
			sources: [
				{ label: 'Work', events: [ev(1), ev(2)], tickets: [], more: 0 },
				{ label: 'Personal', error: "couldn't reach Google", events: [], tickets: [], more: 0 }
			]
		};
		expect(withoutEvent(result, 2).sources).toEqual([
			{ label: 'Work', events: [ev(1)], tickets: [], more: 0 },
			{ label: 'Personal', error: "couldn't reach Google", events: [], tickets: [], more: 0 }
		]);
	});
});

describe('tickets', () => {
	const tk = (key: string) => ({ key, title: `${key} t`, notes: '', url: `https://j/browse/${key}` });
	const result: PullResult = {
		batch_id: 0,
		sources: [{ label: 'Jira', events: [], tickets: [tk('A-1'), tk('A-2'), tk('A-3')], more: 0 }]
	};

	it('Should pick the ticked tickets in the order shown', () => {
		expect(picked(result, new Set(['A-3', 'A-1']))).toEqual([tk('A-1'), tk('A-3')]);
	});

	it('Should drop a skipped ticket from the result', () => {
		expect(withoutTicket(result, 'A-2').sources[0].tickets).toEqual([tk('A-1'), tk('A-3')]);
	});
});

describe('sentLabel', () => {
	it.each([
		['a send earlier today', new Date(2026, 9, 23, 8, 2), new Date(2026, 9, 23, 14, 0), 'today 8:02am'],
		['an afternoon send', new Date(2026, 9, 23, 13, 30), new Date(2026, 9, 23, 14, 0), 'today 1:30pm'],
		['a send on another day', new Date(2026, 9, 21, 8, 2), new Date(2026, 9, 23, 14, 0), 'Oct 21, 8:02am'],
		['a send just before midnight', new Date(2026, 9, 22, 23, 59), new Date(2026, 9, 23, 0, 1), 'Oct 22, 11:59pm']
	])('Should label %s', (_, sent, now, want) => {
		expect(sentLabel(sent.toISOString(), now)).toBe(want);
	});
});
