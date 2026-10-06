import { describe, expect, it } from 'vitest';
import type { Task } from './api';
import { blankForm, formFor, fieldsFrom } from './taskForm';

const task: Task = {
	id: 7,
	title: 'Review FLEX1-666',
	notes: 'needs pubsub research',
	raw_text: 'do FLEX1-666 today, it feels hard',
	size: 'M',
	energy: null,
	feeling: 'feels hard',
	due_on: '2026-10-06',
	not_before: null,
	status: 'open',
	refs: [{ label: 'FLEX1-666' }],
	created_at: '2026-10-05T15:00:00Z',
	updated_at: '2026-10-05T15:00:00Z'
};

describe('task form', () => {
	it('Should round-trip a task unchanged, refs included', () => {
		expect(fieldsFrom(formFor(task), task)).toEqual({
			fields: {
				title: 'Review FLEX1-666',
				notes: 'needs pubsub research',
				size: 'M',
				energy: null,
				feeling: 'feels hard',
				due_on: '2026-10-06',
				not_before: null,
				refs: [{ label: 'FLEX1-666' }]
			}
		});
	});

	it('Should clear fields left empty, and trim the title', () => {
		const form = { ...formFor(task), title: '  Review it  ', size: '' as const, due_on: '' };
		expect(fieldsFrom(form, task)).toMatchObject({ fields: { title: 'Review it', size: null, due_on: null } });
	});

	it('Should send an empty refs list for a task with none', () => {
		expect(fieldsFrom(formFor({ ...task, refs: null }), { ...task, refs: null })).toMatchObject({ fields: { refs: [] } });
	});

	it('Should refuse a blank title before any request', () => {
		expect(fieldsFrom({ ...formFor(task), title: '   ' }, task)).toEqual({ error: 'A title is required.' });
	});
});

describe('new task form', () => {
	it('Should make a plain task from just a title, with nothing else set', () => {
		expect(fieldsFrom({ ...blankForm(), title: 'Call the bank' }, null)).toEqual({
			fields: {
				title: 'Call the bank',
				notes: '',
				size: null,
				energy: null,
				feeling: '',
				due_on: null,
				not_before: null,
				refs: []
			}
		});
	});

	it('Should refuse a new task with no title', () => {
		expect(fieldsFrom(blankForm(), null)).toEqual({ error: 'A title is required.' });
	});
});
