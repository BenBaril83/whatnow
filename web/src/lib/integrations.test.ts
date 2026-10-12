import { describe, expect, it } from 'vitest';
import type { Field, Integration, PullResult } from './api';
import { formFrom, isOn, newRow, withoutEvent } from './integrations';

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
	fields: [{ key: 'team', label: 'Team', kind: 'text', required: false }, { key: 'token', label: 'Token', kind: 'secret', required: false }, calendars],
	enabled: true,
	ready: true,
	values: { team: 'Platform' },
	secrets: { token: true },
	lists: { calendars: [{ id: 'r1', values: { label: 'Work' }, secrets: { url: true } }] },
	skipped: []
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
				{ label: 'Work', events: [ev(1), ev(2)] },
				{ label: 'Personal', error: "couldn't reach Google", events: [] }
			]
		};
		expect(withoutEvent(result, 2).sources).toEqual([
			{ label: 'Work', events: [ev(1)] },
			{ label: 'Personal', error: "couldn't reach Google", events: [] }
		]);
	});
});
