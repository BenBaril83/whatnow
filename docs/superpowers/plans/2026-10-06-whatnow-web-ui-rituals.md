# whatnow web UI, part 2 (rituals and All tasks) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the daily loop in the web UI: a `/morning` check-in, a `/wrap` wrap-up, an All tasks page with an edit sheet, and toasts that show their countdown.

**Architecture:** Route groups give two shells. `(app)` is Today and All tasks, with the header and chat dock. `(ritual)` is the morning and wrap pages, with progress dots, a capture bar, and the chat tab. A shared `live` module owns the event stream, the settings, the minute clock, and the midnight catch-up, so every page sees the same state. New logic lives in small tested modules: `triage.ts`, `tasks.ts`, `taskForm.ts`, and additions to `time.ts` and `toasts.svelte.ts`.

**Tech Stack:** SvelteKit 3 and Svelte 5 runes; TypeScript strict; Vitest 4. No backend changes.

**Spec:** `docs/superpowers/specs/2026-10-06-whatnow-web-ui-rituals-design.md`. It builds on `docs/superpowers/specs/2026-10-05-whatnow-web-ui-today-design.md`.

## Global Constraints

- SvelteKit 3 `#lib/...` imports need the `.ts` extension for `.ts` modules (`'#lib/api.ts'`). Relative imports inside `src/lib` stay extensionless.
- Svelte 5 runes only. Shared state is a class with `$state` fields in a `.svelte.ts` module.
- Components use the `tokens.css` tokens, never raw colors.
- Every non-GET request sends JSON, even `{}`. `api.ts`'s `request` already does this.
- UI copy is American English, with no em dashes.
- The backend doesn't change. `go test ./...` stays green untouched.
- Never save an API key during manual testing. A saved key goes to the real OS keychain.
- Run npm commands from `web/`, or with `npm --prefix web`.

## Review Focus

1. **A task changes elsewhere mid-ritual** (finished in another tab): start or wrap gets a 409. The page shows "Something changed, here's the fresh list," and keeps the choices for tasks still listed. `choicesFor`'s kept-choices test is in Task 1. The page path is a Task 7 smoke step.
2. **A tab open across midnight:** when the window regains focus, it refetches, and a new day with no plan goes to `/morning`. `morningDue` is tested in Task 1. The focus wiring is a Task 7 smoke step.
3. **A double click or a stale tab on `/morning` after the day started:** it redirects to Today and never posts start again. Task 7 smoke step.
4. **Editing a task that has refs:** the refs survive the full `PUT`. Tested in Task 1 (`fieldsFrom`).
5. **Hovering a toast to reach Undo:** the countdown stands still, then resumes for only the time it had left. Tested in Task 2.

---

### Task 1: API additions and the ritual and task logic

**Files:**
- Modify: `web/src/lib/api.ts`, `web/src/lib/time.ts`, `web/src/lib/time.test.ts`, `web/src/lib/plan.test.ts`
- Create: `web/src/lib/triage.ts`, `web/src/lib/triage.test.ts`, `web/src/lib/tasks.ts`, `web/src/lib/tasks.test.ts`, `web/src/lib/taskForm.ts`, `web/src/lib/taskForm.test.ts`

**Interfaces:**
- Produces, from `api.ts`:
  - `Task` gains `notes`, `due_on`, `refs`, and `created_at`.
  - `Day` gains `wrapped_at`, `tomorrow_note`, and `summary`.
  - New types: `Ref`, `TaskFields`, `Blocker`, `Action`, `Suggestion`, `Review`, and `Triage`.
  - New calls: `api.updateTask(id, fields)`, `api.blockers()`, `api.addBlocker(id, by)`, `api.removeBlocker(id, by)`, `api.morning(): {review}`, `api.start(pace, mood, triage): {day, plan}` (now takes the triage), `api.wrapReview(): Review`, and `api.wrap(triage, note): {batch_id, data: Day}`.
- Produces, from `triage.ts`: `Choice {action, until}`, `Choices`, `choicesFor(review, tomorrow, kept?)`, and `triagePayload(choices): Triage[]`.
- Produces, from `tasks.ts`: `Filter`, `filters`, and `listTasks(tasks, blockers, filter, today)`.
- Produces, from `taskForm.ts`: `TaskForm`, `formFor(task)`, and `fieldsFrom(form, task): {fields} | {error}`.
- Produces, from `time.ts`: `wrapDue(now, endOfDay)` and `morningDue(seen, date, planned)`.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/triage.test.ts`:
```ts
import { describe, expect, it } from 'vitest';
import type { Review, Task } from './api';
import { choicesFor, triagePayload } from './triage';

const task = (id: number): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: null,
	not_before: null,
	status: 'open',
	refs: null,
	created_at: '2026-10-01T15:00:00Z',
	updated_at: '2026-10-01T15:00:00Z'
});

const review = (ids: number[], over: Partial<Review> = {}): Review => ({
	day: { date: '2026-10-05', pace: 3, wrapped_at: null, tomorrow_note: '', summary: '' },
	done: [],
	unfinished: ids.map(task),
	suggestions: [
		{ task_id: 1, reason: 'due today', action: 'roll_over', until: '' },
		{ task_id: 2, reason: 'weekend job', action: 'push', until: '2026-10-10' },
		{ task_id: 3, reason: 'pushed 5 times', action: 'drop', until: '' }
	],
	fallback: false,
	...over
});

describe('choicesFor', () => {
	it('Should prefill each task from its suggestion', () => {
		expect(choicesFor(review([1, 2, 3]), '2026-10-06')).toEqual({
			1: { action: 'roll_over', until: '2026-10-06' },
			2: { action: 'push', until: '2026-10-10' },
			3: { action: 'drop', until: '2026-10-06' }
		});
	});

	it('Should roll over a task with no suggestion, ready to push to tomorrow', () => {
		expect(choicesFor(review([4]), '2026-10-06')).toEqual({ 4: { action: 'roll_over', until: '2026-10-06' } });
	});

	it("Should keep the user's choices for tasks still on a refetched review, and drop the rest", () => {
		const kept = { 1: { action: 'drop' as const, until: '2026-10-06' }, 9: { action: 'drop' as const, until: '2026-10-06' } };
		expect(choicesFor(review([1, 2]), '2026-10-06', kept)).toEqual({
			1: { action: 'drop', until: '2026-10-06' },
			2: { action: 'push', until: '2026-10-10' }
		});
	});
});

describe('triagePayload', () => {
	it('Should send until only for a push', () => {
		const got = triagePayload({
			1: { action: 'roll_over', until: '2026-10-06' },
			2: { action: 'push', until: '2026-10-10' },
			3: { action: 'drop', until: '2026-10-06' }
		});
		expect(got).toEqual([
			{ task_id: 1, action: 'roll_over', until: null },
			{ task_id: 2, action: 'push', until: '2026-10-10' },
			{ task_id: 3, action: 'drop', until: null }
		]);
	});

	it('Should send a push with a cleared date as tomorrow, by leaving until out', () => {
		expect(triagePayload({ 2: { action: 'push', until: '' } })).toEqual([
			{ task_id: 2, action: 'push', until: null }
		]);
	});
});
```

`web/src/lib/tasks.test.ts`:
```ts
import { describe, expect, it } from 'vitest';
import type { Blocker, Status, Task } from './api';
import { listTasks, type Filter } from './tasks';

const task = (id: number, over: Partial<Task> = {}): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: null,
	not_before: null,
	status: 'open' as Status,
	refs: null,
	created_at: `2026-10-0${id}T15:00:00Z`,
	updated_at: '2026-10-05T15:00:00Z',
	...over
});

const tasks = [
	task(1),
	task(2, { not_before: '2026-10-09' }),
	task(3, { status: 'done' }),
	task(4, { status: 'dropped' }),
	task(5),
	task(6, { not_before: '2026-10-05' }) // due back today: not future
];
// 5 waits on 1, which is open; 6 waited on 3, which is done.
const blockers: Blocker[] = [
	{ task_id: 5, blocked_by_id: 1 },
	{ task_id: 6, blocked_by_id: 3 }
];

describe('listTasks', () => {
	it.each([
		['open', [6, 5, 2, 1]],
		['future', [2]],
		['blocked', [5]],
		['done', [3]],
		['dropped', [4]],
		['all', [6, 5, 4, 3, 2, 1]]
	] as [Filter, number[]][])('Should show the right tasks for %s', (filter, want) => {
		expect(listTasks(tasks, blockers, filter, '2026-10-05').map((t) => t.id)).toEqual(want);
	});

	it('Should sort by due date, earliest first and undated last, then newest first', () => {
		const got = listTasks(
			[task(1), task(2, { due_on: '2026-10-20' }), task(3, { due_on: '2026-10-07' }), task(4)],
			[],
			'open',
			'2026-10-05'
		);
		expect(got.map((t) => t.id)).toEqual([3, 2, 4, 1]);
	});
});
```

`web/src/lib/taskForm.test.ts`:
```ts
import { describe, expect, it } from 'vitest';
import type { Task } from './api';
import { formFor, fieldsFrom } from './taskForm';

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
```

In `web/src/lib/time.test.ts`, change the import to `import { addDays, countdown, morningDue, strip, survived, wrapDue } from './time';` and append:
```ts
describe('wrapDue', () => {
	it.each([
		['16:29', false],
		['16:30', true],
		['20:00', true]
	])('Should offer wrap-up at %s with a 17:30 end of day: %s', (now, want) => {
		expect(wrapDue(at(now), at('17:30'))).toBe(want);
	});
});

describe('morningDue', () => {
	it.each([
		['the date changed and today has no plan', '2026-10-05', '2026-10-06', false, true],
		['the date changed but today already has a plan', '2026-10-05', '2026-10-06', true, false],
		['the same date with no plan', '2026-10-06', '2026-10-06', false, false]
	])('Should know whether to go to the morning when %s', (_, seen, date, planned, want) => {
		expect(morningDue(seen, date, planned)).toBe(want);
	});
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test`
Expected: FAIL: cannot resolve `./triage`, `./tasks`, and `./taskForm`, and `wrapDue`/`morningDue` are not functions.

- [ ] **Step 3: Implement**

Replace `web/src/lib/api.ts` with:
```ts
// Types mirror the Go JSON. Only the fields the client reads are listed.

export type Size = 'S' | 'M' | 'L';
export type Energy = 'low' | 'med' | 'high';
export type Status = 'open' | 'done' | 'dropped';
export type Tier = 'must' | 'should' | 'could';
export type Source = 'user' | 'chat' | 'cleanup' | 'triage';
export type Provider = 'anthropic' | 'openai';
/** Where a provider's key comes from. Empty means there is none. */
export type KeySource = '' | 'keychain' | 'env';

/** A link or key pulled out of a task's text. */
export interface Ref {
	label: string;
	url?: string;
}

export interface Task {
	id: number;
	title: string;
	notes: string;
	raw_text: string;
	size: Size | null;
	energy: Energy | null;
	feeling: string;
	due_on: string | null;
	/** A push sets this. The task is put off until then. */
	not_before: string | null;
	status: Status;
	refs: Ref[] | null;
	created_at: string;
	updated_at: string;
}

/** What a task write sends. Every field is sent; null clears one. */
export interface TaskFields {
	title: string;
	notes: string;
	size: Size | null;
	energy: Energy | null;
	feeling: string;
	due_on: string | null;
	not_before: string | null;
	refs: Ref[];
}

/** TaskID can't start until BlockedByID is finished. */
export interface Blocker {
	task_id: number;
	blocked_by_id: number;
}

export interface CalendarEvent {
	id: number;
	title: string;
	starts_at: string;
	ends_at: string | null;
}

export interface Day {
	date: string;
	pace: number;
	wrapped_at: string | null;
	tomorrow_note: string;
	summary: string;
}

export type Action = 'roll_over' | 'push' | 'drop';

/** The model's advice for one unfinished task. Until is set only for a push. */
export interface Suggestion {
	task_id: number;
	reason: string;
	action: Action;
	until: string;
}

/** A day's outcome, for wrap-up and the morning's reconcile. Fallback means code suggested. */
export interface Review {
	day: Day;
	done: Task[];
	unfinished: Task[];
	suggestions: Suggestion[];
	fallback: boolean;
}

/** What the user decided for one unfinished task. A push with no until goes to tomorrow. */
export interface Triage {
	task_id: number;
	action: Action;
	until: string | null;
}

/** The five pace levels, 1 to 5, as the server labels them. */
export const paceLabels = ['running on fumes', 'gentle', 'steady', 'full steam', 'go hard'];

export interface PlanEntry {
	task_id: number;
	tier: Tier;
	rationale: string;
}

export interface Plan {
	id: number;
	opening: string;
	entries: PlanEntry[];
	fallback: boolean;
}

export interface Today {
	date: string;
	day: Day | null;
	plan: Plan | null;
	unplanned: number[];
	blocked: number[];
}

export interface Pick {
	task_id: number;
	reason: string;
}

export interface ToolUse {
	tool: string;
	label: string;
	error: string;
}

export interface Message {
	id: number;
	role: 'user' | 'assistant';
	text: string;
	tools: ToolUse[] | null;
	/** The reply's changes, or 0 when it changed nothing. */
	batch_id: number;
	/** Set on a reply the provider couldn't give. */
	error: string;
}

/** The response to every write. */
export interface Mutation {
	batch_id: number;
}

export interface BatchApplied {
	batch_id: number;
	source: Source;
}

export interface ProviderConfig {
	main_model: string;
	light_model: string;
	base_url: string;
}

export interface Settings {
	provider: Provider | '';
	anthropic: ProviderConfig;
	openai: ProviderConfig;
	style: string;
	about_me: string;
	end_of_day: string;
}

export interface SettingsView {
	settings: Settings;
	keys: Record<Provider, KeySource>;
}

export interface ConnectionResult {
	ok: boolean;
	error?: string;
}

export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
	}
}

// Every write sends a JSON body, even an empty one: the server rejects any other write as a
// possible cross-site form post, and decodes a body on most writes.
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const init: RequestInit = { method };
	if (method !== 'GET') {
		init.headers = { 'Content-Type': 'application/json' };
		init.body = JSON.stringify(body ?? {});
	}
	const res = await fetch(`/api/v1${path}`, init);
	if (res.status === 204) return undefined as T;
	const data = await res.json().catch(() => ({}));
	if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText);
	return data as T;
}

export const api = {
	today: () => request<Today>('GET', '/today'),
	events: (date: string) => request<CalendarEvent[]>('GET', `/events?date=${date}`),
	tasks: () => request<Task[]>('GET', '/tasks'),
	chat: () => request<Message[]>('GET', '/chat'),
	capture: (raw_text: string) => request<Mutation>('POST', '/tasks', { raw_text }),
	setStatus: (id: number, status: Status) =>
		request<Mutation>('POST', `/tasks/${id}/status`, { status }),
	push: (id: number, until: string) => request<Mutation>('POST', `/tasks/${id}/push`, { until }),
	undo: (batch: number) => request<void>('POST', `/batches/${batch}/undo`),
	setPace: (pace: number) => request<Mutation>('PUT', '/today/pace', { pace, note: '' }),
	updateTask: (id: number, fields: TaskFields) => request<Mutation>('PUT', `/tasks/${id}`, fields),
	blockers: () => request<Blocker[]>('GET', '/blockers'),
	addBlocker: (id: number, blocked_by_id: number) =>
		request<Mutation>('POST', `/tasks/${id}/blockers`, { blocked_by_id }),
	removeBlocker: (id: number, blockedBy: number) =>
		request<Mutation>('DELETE', `/tasks/${id}/blockers/${blockedBy}`),
	morning: () => request<{ review: Review | null }>('GET', '/morning'),
	start: (pace: number, mood_note: string, triage: Triage[]) =>
		request<{ day: Day; plan: Plan }>('POST', '/today/start', { pace, mood_note, triage }),
	wrapReview: () => request<Review>('GET', '/today/wrap'),
	wrap: (triage: Triage[], note: string) =>
		request<{ batch_id: number; data: Day }>('POST', '/today/wrap', { triage, note }),
	whatNow: (exclude_task_id = 0, reason = '') =>
		request<Pick>('POST', '/whatnow', { exclude_task_id, reason }),
	send: (text: string) => request<Message>('POST', '/chat', { text }),
	settings: () => request<SettingsView>('GET', '/settings'),
	saveSettings: (s: Settings) => request<SettingsView>('PUT', '/settings', s),
	saveKey: (provider: Provider, key: string) =>
		request<void>('PUT', `/settings/keys/${provider}`, { key }),
	testConnection: () => request<ConnectionResult>('POST', '/settings/test')
};

/** Whether a model is set up. Without one, capture still works and plans come from code. */
export function configured(view: SettingsView): boolean {
	const p = view.settings.provider;
	return p !== '' && view.keys[p] !== '';
}
```

Append to `web/src/lib/time.ts`:
```ts
/** Wrap-up is offered from an hour before the end of day. */
export function wrapDue(now: Date, endOfDay: Date): boolean {
	return now.getTime() >= endOfDay.getTime() - 60 * minute;
}

/**
 * Whether a tab that last saw `seen` should start the morning now: the date has moved on and the
 * new day has no plan yet. A tab left open overnight otherwise keeps showing yesterday.
 */
export function morningDue(seen: string, date: string, planned: boolean): boolean {
	return date !== seen && !planned;
}
```

`web/src/lib/triage.ts`:
```ts
import type { Action, Review, Triage } from './api';

/** One task's decision. Until is the push date, kept even while another action is picked. */
export interface Choice {
	action: Action;
	until: string;
}

export type Choices = Record<number, Choice>;

/**
 * The choices for a review's unfinished tasks: what the user already picked for a task still on
 * it, else the model's suggestion, else roll over. A push date defaults to tomorrow.
 */
export function choicesFor(review: Review, tomorrow: string, kept: Choices = {}): Choices {
	const suggested = new Map(review.suggestions.map((s) => [s.task_id, s]));
	const out: Choices = {};
	for (const task of review.unfinished) {
		const s = suggested.get(task.id);
		out[task.id] = kept[task.id] ?? {
			action: s?.action ?? 'roll_over',
			until: s?.action === 'push' && s.until ? s.until : tomorrow
		};
	}
	return out;
}

/** The triage to send. An empty push date leaves until out, which the server reads as tomorrow. */
export function triagePayload(choices: Choices): Triage[] {
	return Object.entries(choices).map(([id, c]) => ({
		task_id: Number(id),
		action: c.action,
		until: c.action === 'push' && c.until ? c.until : null
	}));
}
```

`web/src/lib/tasks.ts`:
```ts
import type { Blocker, Task } from './api';

export type Filter = 'open' | 'future' | 'blocked' | 'done' | 'dropped' | 'all';

export const filters: { id: Filter; label: string }[] = [
	{ id: 'open', label: 'Open' },
	{ id: 'future', label: 'Future' },
	{ id: 'blocked', label: 'Blocked' },
	{ id: 'done', label: 'Done' },
	{ id: 'dropped', label: 'Dropped' },
	{ id: 'all', label: 'All' }
];

/** The tasks a filter shows, sorted by due date (undated last), then newest first. */
export function listTasks(tasks: Task[], blockers: Blocker[], filter: Filter, today: string): Task[] {
	const open = new Set(tasks.filter((t) => t.status === 'open').map((t) => t.id));
	const blocked = new Set(blockers.filter((b) => open.has(b.blocked_by_id)).map((b) => b.task_id));
	const keep: Record<Filter, (t: Task) => boolean> = {
		open: (t) => t.status === 'open',
		future: (t) => t.status === 'open' && (t.not_before ?? '') > today,
		blocked: (t) => t.status === 'open' && blocked.has(t.id),
		done: (t) => t.status === 'done',
		dropped: (t) => t.status === 'dropped',
		all: () => true
	};
	return tasks.filter(keep[filter]).sort((a, b) => {
		if (a.due_on !== b.due_on) {
			if (!a.due_on) return 1;
			if (!b.due_on) return -1;
			return a.due_on < b.due_on ? -1 : 1;
		}
		return b.created_at.localeCompare(a.created_at);
	});
}
```

`web/src/lib/taskForm.ts`:
```ts
import type { Energy, Size, Task, TaskFields } from './api';

/** The edit form's values. Every field is a string; empty means unset. */
export interface TaskForm {
	title: string;
	notes: string;
	size: Size | '';
	energy: Energy | '';
	feeling: string;
	due_on: string;
	not_before: string;
}

export function formFor(task: Task): TaskForm {
	return {
		title: task.title,
		notes: task.notes,
		size: task.size ?? '',
		energy: task.energy ?? '',
		feeling: task.feeling,
		due_on: task.due_on ?? '',
		not_before: task.not_before ?? ''
	};
}

/**
 * The write for a form. The server replaces every field, so the task's refs go back unchanged. A
 * blank title is refused here, since the server's message for it would mention raw text.
 */
export function fieldsFrom(form: TaskForm, task: Task): { fields: TaskFields } | { error: string } {
	const title = form.title.trim();
	if (!title) return { error: 'A title is required.' };
	return {
		fields: {
			title,
			notes: form.notes,
			size: form.size || null,
			energy: form.energy || null,
			feeling: form.feeling,
			due_on: form.due_on || null,
			not_before: form.not_before || null,
			refs: task.refs ?? []
		}
	};
}
```

`plan.test.ts` builds `Task` and `Day` literals, so give its fixtures the new fields. Replace its `task` helper with:
```ts
const task = (id: number, status: Status = 'open', updated_at = '2026-10-05T15:00:00Z'): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: null,
	not_before: null,
	status,
	refs: null,
	created_at: '2026-10-01T15:00:00Z',
	updated_at
});
```
and its `day:` line with `day: { date: '2026-10-05', pace: 3, wrapped_at: null, tomorrow_note: '', summary: '' },`.

`api.start` now takes a triage, so `StartDay.svelte` stops type-checking. Task 3 deletes it, so don't fix it. Expect `npm run check` to flag only that file until then.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix web test`
Expected: every test passes.

- [ ] **Step 5: Commit**
```bash
git add web/src/lib
git commit -m "Add the API calls and logic for the rituals and All tasks"
```

---

### Task 2: Toasts show their countdown and pause on hover

**Files:**
- Modify: `web/src/lib/toasts.svelte.ts`, `web/src/lib/toasts.test.ts`, `web/src/lib/components/Toasts.svelte`

**Interfaces:**
- Produces: `lifetime` (exported, in ms), `Toast.paused`, `toasts.pause(id)`, and `toasts.resume(id)`. `dismiss` and `undone` also clear their timers.

- [ ] **Step 1: Write the failing test.** Append inside the `describe('Toasts', ...)` block of `web/src/lib/toasts.test.ts`:
```ts
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm --prefix web test -- src/lib/toasts.test.ts`
Expected: FAIL: `t.pause is not a function`.

- [ ] **Step 3: Implement.** Replace `web/src/lib/toasts.svelte.ts` with:
```ts
import { api, type BatchApplied, type Mutation } from './api';

export interface Toast {
	id: number;
	text: string;
	/** The batch Undo reverts. Absent on an error. */
	batch?: number;
	error?: boolean;
	/** Set while hovered or focused: the countdown and its fill stand still. */
	paused?: boolean;
}

/** How long a toast stays, in ms. Its fill sweeps across in the same time. */
export const lifetime = 8000;

interface Timer {
	handle: ReturnType<typeof setTimeout>;
	endsAt: number;
	left: number;
}

export class Toasts {
	items = $state<Toast[]>([]);
	#next = 1;
	#timers = new Map<number, Timer>();

	#add(toast: Omit<Toast, 'id'>): void {
		const id = this.#next++;
		this.items.push({ id, ...toast });
		this.#run(id, lifetime);
	}

	#run(id: number, ms: number): void {
		this.#timers.set(id, { handle: setTimeout(() => this.dismiss(id), ms), endsAt: Date.now() + ms, left: ms });
	}

	/** Stops a toast's countdown where it is, so reaching Undo is never a race. */
	pause(id: number): void {
		const timer = this.#timers.get(id);
		const toast = this.items.find((t) => t.id === id);
		if (!timer || !toast || toast.paused) return;
		clearTimeout(timer.handle);
		timer.left = Math.max(0, timer.endsAt - Date.now());
		toast.paused = true;
	}

	/** Restarts a paused countdown for only the time it had left. */
	resume(id: number): void {
		const timer = this.#timers.get(id);
		const toast = this.items.find((t) => t.id === id);
		if (!timer || !toast?.paused) return;
		toast.paused = false;
		this.#run(id, timer.left);
	}

	show(text: string, batch: number): void {
		this.#add({ text, batch });
	}

	error(err: unknown): void {
		this.#add({ text: err instanceof Error ? err.message : String(err), error: true });
	}

	dismiss(id: number): void {
		clearTimeout(this.#timers.get(id)?.handle);
		this.#timers.delete(id);
		this.items = this.items.filter((t) => t.id !== id);
	}

	/** Runs a write and offers its undo, or shows why it failed. Returns whether it worked. */
	async track(text: string, write: () => Promise<Mutation>): Promise<boolean> {
		try {
			this.show(text, (await write()).batch_id);
			return true;
		} catch (err) {
			this.error(err);
			return false;
		}
	}

	/**
	 * Only cleanup's batches get a toast from the stream. A user write gets its toast from its own
	 * response, a chat batch's undo is on its reply, and triage belongs to the morning and wrap-up
	 * flows.
	 */
	applied(b: BatchApplied): void {
		if (b.source === 'cleanup') this.show("Here's what I did", b.batch_id);
	}

	undone(batch: number): void {
		for (const t of this.items.filter((t) => t.batch === batch)) this.dismiss(t.id);
	}

	async undo(toast: Toast): Promise<void> {
		if (toast.batch === undefined) return;
		try {
			await api.undo(toast.batch);
		} catch (err) {
			this.error(err);
		}
	}
}

export const toasts = new Toasts();
```

Replace `web/src/lib/components/Toasts.svelte` with:
```svelte
<script lang="ts">
	import { lifetime, toasts } from '#lib/toasts.svelte.ts';
</script>

<div class="stack" aria-live="polite">
	{#each toasts.items as toast (toast.id)}
		<div
			class="sticker toast"
			class:error={toast.error}
			class:paused={toast.paused}
			style:--lifetime="{lifetime}ms"
			role="status"
			onpointerenter={() => toasts.pause(toast.id)}
			onpointerleave={() => toasts.resume(toast.id)}
			onfocusin={() => toasts.pause(toast.id)}
			onfocusout={() => toasts.resume(toast.id)}
		>
			<span class="fill" aria-hidden="true"></span>
			<span class="text">{toast.text}</span>
			{#if toast.batch !== undefined}
				<button class="btn" onclick={() => toasts.undo(toast)}>Undo</button>
			{/if}
			<button class="close" aria-label="Dismiss" onclick={() => toasts.dismiss(toast.id)}>✕</button>
		</div>
	{/each}
</div>

<style>
	.stack {
		position: fixed;
		left: 16px;
		bottom: 16px;
		display: flex;
		flex-direction: column;
		gap: 8px;
		z-index: 30;
		max-width: min(420px, calc(100vw - 32px));
	}
	.toast {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		font-weight: var(--heavy);
	}
	.toast {
		position: relative;
		overflow: hidden;
	}
	.text {
		flex: 1;
	}
	.toast > :not(.fill) {
		position: relative;
	}
	/* Sweeps left to right over the toast's lifetime, so you can see how long it has left. */
	.fill {
		position: absolute;
		inset: 0;
		background: var(--primary);
		opacity: 0.14;
		transform-origin: left;
		animation: fill var(--lifetime) linear forwards;
	}
	.paused .fill {
		animation-play-state: paused;
	}
	@keyframes fill {
		from {
			transform: scaleX(0);
		}
		to {
			transform: scaleX(1);
		}
	}
	.error {
		background: var(--must);
	}
	.close {
		background: none;
		border: none;
		padding: 0 4px;
	}
</style>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix web test`
Expected: every test passes.

- [ ] **Step 5: Commit**
```bash
git add web/src/lib
git commit -m "Show each toast's countdown, and pause it on hover"
```

---

### Task 3: Shared live state, the app shell, and the reworked Today

**Files:**
- Create: `web/src/lib/live.svelte.ts`, `web/src/lib/editor.svelte.ts`, `web/src/lib/components/DaysDone.svelte`, `web/src/routes/(app)/+layout.svelte`
- Move: `web/src/routes/+page.svelte` → `web/src/routes/(app)/+page.svelte` (with `git mv`), then replace its contents
- Modify: `web/src/lib/today.svelte.ts`, `web/src/lib/today.test.ts`, `web/src/lib/components/Header.svelte`, `web/src/routes/+layout.svelte`, `web/src/routes/settings/+page.svelte`
- Delete: `web/src/lib/components/StartDay.svelte`

**Interfaces:**
- Consumes: `wrapDue` and `morningDue` from Task 1.
- Produces:
  - `today.blockers: Blocker[]`, refreshed with the rest.
  - `live.view`, `live.now`, `live.connected`, `live.endOfDay`, and `live.start(): () => void`.
  - `editor.taskId`, `editor.open(id)`, and `editor.close()`.
  - `Header {today, asking?, onwhatnow?}`. What now? shows only when `onwhatnow` is passed.
  - `DaysDone {day, done, meetings}`.

- [ ] **Step 1: Write the failing test.** In `web/src/lib/today.test.ts`, add `vi.spyOn(api, 'blockers').mockResolvedValue([]);` after each `vi.spyOn(api, 'tasks')` line, then add:
```ts
	it('Should refresh the blockers with everything else', async () => {
		vi.spyOn(api, 'today').mockResolvedValue(day);
		vi.spyOn(api, 'events').mockResolvedValue([]);
		vi.spyOn(api, 'tasks').mockResolvedValue([]);
		vi.spyOn(api, 'blockers').mockResolvedValue([{ task_id: 2, blocked_by_id: 1 }]);
		const t = new TodayState();
		await t.refresh();
		expect(t.blockers).toEqual([{ task_id: 2, blocked_by_id: 1 }]);
	});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `npm --prefix web test -- src/lib/today.test.ts`
Expected: FAIL: `t.blockers` is `undefined`.

- [ ] **Step 3: Implement the state.** Replace `web/src/lib/today.svelte.ts` with:
```ts
import { api, type Blocker, type CalendarEvent, type Task, type Today } from './api';
import { toasts } from './toasts.svelte';

const debounce = 100;

/** Today's state, refetched whole whenever the server says something changed. */
export class TodayState {
	today = $state<Today | null>(null);
	events = $state<CalendarEvent[]>([]);
	tasks = $state<Task[]>([]);
	blockers = $state<Blocker[]>([]);
	#timer: ReturnType<typeof setTimeout> | undefined;

	async refresh(): Promise<void> {
		const today = await api.today();
		const [events, tasks, blockers] = await Promise.all([
			api.events(today.date),
			api.tasks(),
			api.blockers()
		]);
		this.today = today;
		this.events = events;
		this.tasks = tasks;
		this.blockers = blockers;
	}

	/** Refreshes once a burst of changes settles. */
	changed(): void {
		clearTimeout(this.#timer);
		this.#timer = setTimeout(() => this.refresh().catch((err) => toasts.error(err)), debounce);
	}
}

export const today = new TodayState();
```

`web/src/lib/live.svelte.ts`:
```ts
import { goto } from '$app/navigation';
import { api, type SettingsView } from './api';
import { chat } from './chat.svelte';
import { Stream } from './stream.svelte';
import { morningDue } from './time';
import { toasts } from './toasts.svelte';
import { today } from './today.svelte';

/**
 * The page's live connection to the server, shared by every screen: the event stream, the
 * settings, and a clock that ticks each minute.
 */
class Live {
	view = $state<SettingsView | null>(null);
	now = $state(new Date());

	#stream = new Stream(
		{
			'batch.applied': (d) => {
				today.changed();
				toasts.applied(d);
			},
			'batch.undone': (d) => {
				today.changed();
				toasts.undone(d.batch_id);
				chat.markUndone(d.batch_id);
			},
			'plan.updated': () => today.changed(),
			'chat.delta': (d) => chat.delta(d.text),
			'chat.tool': (d) => chat.tool(d),
			'chat.message': (d) => chat.message(d)
		},
		() => {
			today.changed();
			chat.load().catch((err) => toasts.error(err));
		}
	);

	get connected(): boolean {
		return this.#stream.connected;
	}

	/** When today's working day ends, from the settings. */
	get endOfDay(): Date {
		const date = today.today?.date ?? '';
		return new Date(`${date}T${this.view?.settings.end_of_day ?? '17:30'}:00`);
	}

	/** Connects once for the life of the page. Returns the disconnect. */
	start(): () => void {
		Promise.all([today.refresh(), chat.load(), api.settings().then((v) => (this.view = v))]).catch(
			(err) => toasts.error(err)
		);
		this.#stream.open();
		const clock = setInterval(() => (this.now = new Date()), 60_000);
		const focus = () => this.#caughtUp();
		window.addEventListener('focus', focus);
		return () => {
			clearInterval(clock);
			window.removeEventListener('focus', focus);
			this.#stream.close();
		};
	}

	// A tab left open overnight still shows yesterday. Coming back to it catches up, and starts the
	// morning when the new day has no plan yet.
	async #caughtUp(): Promise<void> {
		this.now = new Date();
		const seen = today.today?.date;
		try {
			await today.refresh();
		} catch (err) {
			toasts.error(err);
			return;
		}
		const t = today.today;
		if (seen && t && morningDue(seen, t.date, t.plan !== null)) goto('/morning');
	}
}

export const live = new Live();
```

`web/src/lib/editor.svelte.ts`:
```ts
/** Which task the edit sheet has open, if any. Any card can open it. */
class Editor {
	taskId = $state<number | null>(null);

	open(id: number): void {
		this.taskId = id;
	}

	close(): void {
		this.taskId = null;
	}
}

export const editor = new Editor();
```

- [ ] **Step 4: Run the tests**

Run: `npm --prefix web test`
Expected: every test passes.

- [ ] **Step 5: Build the shell.** The Today page stops owning the stream: `live` does, started once by the root layout.

`git rm web/src/lib/components/StartDay.svelte`, and `git mv web/src/routes/+page.svelte "web/src/routes/(app)/+page.svelte"`.

Replace `web/src/routes/+layout.svelte` with:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import '#lib/tokens.css';
	import favicon from '#lib/assets/favicon.svg';
	import Toasts from '#lib/components/Toasts.svelte';
	import { live } from '#lib/live.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	onMount(() => live.start());
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>whatnow</title>
</svelte:head>

{@render children()}
<Toasts />
```

`web/src/routes/(app)/+layout.svelte`. It references `EditSheet`, which arrives in Task 6, so leave the `EditSheet` import and tag out until then:
```svelte
<script lang="ts">
	import ChatDock from '#lib/components/ChatDock.svelte';
	import EditSheet from '#lib/components/EditSheet.svelte';
	import { today } from '#lib/today.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();
</script>

<div class="page">
	<main>
		{@render children()}
	</main>
	<ChatDock opening={today.today?.plan?.opening ?? ''} />
</div>
<EditSheet />

<style>
	.page {
		display: flex;
		gap: 16px;
		padding: 16px;
		align-items: flex-start;
	}
	main {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}

	@media (max-width: 800px) {
		.page {
			padding: 12px 12px 72px;
		}
	}
</style>
```

Replace `web/src/routes/(app)/+page.svelte` with:
```svelte
<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError, type Pick, type Tier } from '#lib/api.ts';
	import { live } from '#lib/live.svelte.ts';
	import { doneOn, tiers, unplanned } from '#lib/plan.ts';
	import { survived } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';
	import Capture from '#lib/components/Capture.svelte';
	import DayStrip from '#lib/components/DayStrip.svelte';
	import DaysDone from '#lib/components/DaysDone.svelte';
	import DoneToday from '#lib/components/DoneToday.svelte';
	import Header from '#lib/components/Header.svelte';
	import TaskCard from '#lib/components/TaskCard.svelte';
	import WhatNow from '#lib/components/WhatNow.svelte';

	let pick = $state<Pick | null>(null);
	let asking = $state(false);
	/** What now found nothing open and eligible. */
	let nothing = $state(false);

	const t = $derived(today.today);
	const planned = $derived(t ? tiers(t, today.tasks) : null);
	const tray = $derived(t ? unplanned(t, today.tasks) : []);
	const done = $derived(t ? doneOn(t.date, today.tasks) : []);
	const pickTask = $derived(pick ? today.tasks.find((x) => x.id === pick?.task_id) : undefined);
	const tierNames: Tier[] = ['must', 'should', 'could'];

	// A day with no plan starts with the morning check-in.
	$effect(() => {
		if (t && !t.plan) goto('/morning', { replaceState: true });
	});

	async function whatNow(exclude = 0, reason = '') {
		asking = true;
		nothing = false;
		try {
			pick = await api.whatNow(exclude, reason);
		} catch (err) {
			pick = null;
			// 404 means nothing is open and eligible: news to say gently, not an error.
			if (err instanceof ApiError && err.status === 404) nothing = true;
			else toasts.error(err);
		} finally {
			asking = false;
		}
	}
</script>

{#if t?.plan}
	<Header today={t} {asking} onwhatnow={() => whatNow()} />
	<DayStrip events={today.events} now={live.now} endOfDay={live.endOfDay} />
	<Capture />

	{#if pick}
		<WhatNow
			{pick}
			task={pickTask}
			{asking}
			onnotthis={(reason) => whatNow(pick?.task_id, reason)}
			onclose={() => (pick = null)}
		/>
	{:else if nothing}
		<p class="sticker nothing">Nothing to do right now. Enjoy it.</p>
	{/if}

	{#if t.day?.wrapped_at}
		<DaysDone day={t.day} done={done.length} meetings={survived(today.events, live.now)} />
	{:else if planned}
		{#if t.plan.fallback}
			<p class="chip rough">rough plan, model was out</p>
		{/if}
		{#each tierNames as tier (tier)}
			<section>
				<h2 class="label">{tier}</h2>
				{#each planned[tier] as card (card.task.id)}
					<TaskCard {card} {tier} date={t.date} />
				{:else}
					<p class="muted empty">Nothing here.</p>
				{/each}
			</section>
		{/each}
	{/if}

	<div class="lower">
		<section>
			<h2 class="label">Unplanned</h2>
			{#each tray as card (card.task.id)}
				<TaskCard {card} date={t.date} />
			{:else}
				<p class="muted empty">Tray's empty.</p>
			{/each}
		</section>
		<section>
			<DoneToday {done} meetings={survived(today.events, live.now)} />
		</section>
	</div>
{:else}
	<p class="muted">Loading…</p>
{/if}

<style>
	h2 {
		margin: 6px 0 8px;
	}
	.empty {
		margin: 0 0 8px;
		font-size: 14px;
	}
	.nothing {
		margin: 0;
		padding: 12px 16px;
		font-weight: var(--heavy);
	}
	.rough {
		align-self: flex-start;
		margin: 0;
	}
	.lower {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 16px;
		align-items: start;
	}

	@media (max-width: 800px) {
		.lower {
			grid-template-columns: 1fr;
		}
	}
</style>
```

Replace `web/src/lib/components/Header.svelte` with:
```svelte
<script lang="ts">
	import { api, configured, paceLabels, type Today } from '#lib/api.ts';
	import { live } from '#lib/live.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { countdown, wrapDue } from '#lib/time.ts';
	import { today as todayState } from '#lib/today.svelte.ts';

	interface Props {
		today: Today;
		/** Today passes these to offer What now?; other pages leave them out. */
		asking?: boolean;
		onwhatnow?: () => void;
	}
	let { today, asking = false, onwhatnow }: Props = $props();

	let paceOpen = $state(false);
	let menuOpen = $state(false);
	const next = $derived(countdown(todayState.events, live.now));
	const wrapped = $derived(today.day?.wrapped_at != null);
	const canWrap = $derived(today.day !== null && !wrapped);
	const date = $derived(
		new Date(`${today.date}T12:00:00`).toLocaleDateString(undefined, {
			weekday: 'long',
			month: 'short',
			day: 'numeric'
		})
	);

	function setPace(pace: number) {
		paceOpen = false;
		toasts.track(`Pace: ${paceLabels[pace - 1]}`, () => api.setPace(pace));
	}
</script>

<header class="sticker">
	<div class="left">
		<h1>{date}</h1>
		{#if today.day}
			<details class="pace" bind:open={paceOpen}>
				<summary class="chip">pace: {paceLabels[today.day.pace - 1]}</summary>
				<div class="sticker menu">
					{#each paceLabels as label, i (label)}
						<button class="item" class:current={i + 1 === today.day.pace} onclick={() => setPace(i + 1)}>
							{label}
						</button>
					{/each}
				</div>
			</details>
		{/if}
		{#if !live.connected}
			<span class="chip offline">reconnecting…</span>
		{/if}
	</div>
	<div class="right">
		{#if next}
			<span class="next">{next}</span>
		{/if}
		{#if canWrap && wrapDue(live.now, live.endOfDay)}
			<a class="btn" href="/wrap">Wrap up</a>
		{/if}
		{#if onwhatnow && !wrapped}
			<button class="btn primary" disabled={asking || !today.day} onclick={onwhatnow}>
				{asking ? 'Thinking…' : 'What now?'}
			</button>
		{/if}
		<details class="more" bind:open={menuOpen}>
			<summary class="btn" aria-label="Menu">⋯</summary>
			<nav class="sticker menu">
				<a class="item" href="/" onclick={() => (menuOpen = false)}>Today</a>
				<a class="item" href="/tasks" onclick={() => (menuOpen = false)}>All tasks</a>
				{#if canWrap}<a class="item" href="/wrap" onclick={() => (menuOpen = false)}>Wrap up early</a>{/if}
				<a class="item" href="/settings" onclick={() => (menuOpen = false)}>Settings</a>
			</nav>
		</details>
	</div>
	{#if live.view && !configured(live.view)}
		<p class="notice">
			No model set up yet. Capture works and plans come out rough. <a href="/settings">Set one up</a>
		</p>
	{/if}
</header>

<style>
	header {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		padding: 12px 16px;
	}
	.left,
	.right {
		display: flex;
		align-items: center;
		gap: 12px;
	}
	h1 {
		margin: 0;
		font-size: 22px;
		font-weight: var(--heavy);
	}
	.next {
		font-weight: var(--heavy);
	}
	.pace {
		position: relative;
	}
	.pace summary {
		list-style: none;
		cursor: pointer;
	}
	.menu {
		position: absolute;
		top: 32px;
		left: 0;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 170px;
	}
	.item {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.item:hover,
	.item.current {
		background: var(--chip);
	}
	a.btn,
	a.item {
		color: var(--ink);
		text-decoration: none;
	}
	.more {
		position: relative;
	}
	.more summary {
		list-style: none;
	}
	.more .menu {
		left: auto;
		right: 0;
		top: 40px;
	}
	.offline {
		background: var(--must);
	}
	.notice {
		flex-basis: 100%;
		margin: 0;
		font-size: 14px;
	}
</style>
```

`web/src/lib/components/DaysDone.svelte`:
```svelte
<script lang="ts">
	import type { Day } from '#lib/api.ts';
	import { winLine } from '#lib/plan.ts';

	interface Props {
		day: Day;
		done: number;
		meetings: number;
	}
	let { day, done, meetings }: Props = $props();
</script>

<section class="sticker daysdone">
	<p class="label">Day's done</p>
	<h2>{winLine(done, meetings)}</h2>
	{#if day.summary}<p>{day.summary}</p>{/if}
	{#if day.tomorrow_note}
		<p class="note"><span class="label">For tomorrow</span> {day.tomorrow_note}</p>
	{/if}
	<p class="muted">You're off the hook. Anything you capture now waits in the tray for the morning.</p>
</section>

<style>
	.daysdone {
		background: var(--should);
		padding: 18px 22px;
	}
	h2 {
		margin: 4px 0 8px;
		font-weight: var(--heavy);
	}
	.note {
		background: var(--card);
		border: var(--line);
		border-radius: var(--radius-sm);
		padding: 8px 10px;
	}
</style>
```

In `web/src/routes/settings/+page.svelte`, import `live` (`import { live } from '#lib/live.svelte.ts';`), and after `view = await api.saveSettings(view.settings);` add `live.view = view;`. That way the header's no-model notice updates without a reload.

- [ ] **Step 6: Type-check**

Run: `npm --prefix web run check`
Expected: `0 ERRORS 0 WARNINGS`.

- [ ] **Step 7: Commit**
```bash
git add -A web/src
git commit -m "Share live state across pages, and give Today the app shell and a Day's-done card"
```

---

### Task 4: The good-morning ritual

**Files:**
- Create: `web/src/routes/(ritual)/+layout.svelte`, `web/src/routes/(ritual)/morning/+page.svelte`, `web/src/lib/components/Dots.svelte`, `web/src/lib/components/TriageList.svelte`

**Interfaces:**
- Consumes: `api.morning`, `api.start`, `choicesFor`, `triagePayload`, `tiers`, and `today`.
- Produces: `Dots {count, at}` and `TriageList {review, bind:choices}`, both reused by Task 5.

- [ ] **Step 1: Write the components and pages**

`web/src/routes/(ritual)/+layout.svelte`:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import Capture from '#lib/components/Capture.svelte';
	import ChatDock from '#lib/components/ChatDock.svelte';
	import { chat } from '#lib/chat.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// The ritual is calm by default: chat waits as a tab. This doesn't touch the saved choice, so
	// Today opens the dock the way it was.
	onMount(() => {
		chat.open = false;
	});
</script>

<main class="ritual">
	{@render children()}
</main>
<div class="capture">
	<Capture />
</div>
<ChatDock opening="" />

<style>
	.ritual {
		max-width: 640px;
		margin: 0 auto;
		padding: 32px 16px 120px;
		display: flex;
		flex-direction: column;
		gap: 18px;
	}
	.capture {
		position: fixed;
		left: 50%;
		bottom: 16px;
		transform: translateX(-50%);
		width: min(640px, calc(100vw - 32px));
		z-index: 10;
	}

	@media (max-width: 800px) {
		.capture {
			bottom: 64px;
		}
	}
</style>
```

`web/src/lib/components/Dots.svelte`:
```svelte
<script lang="ts">
	interface Props {
		count: number;
		/** Zero-based. */
		at: number;
	}
	let { count, at }: Props = $props();
</script>

<div class="dots" aria-label="Step {at + 1} of {count}">
	{#each { length: count }, i (i)}
		<span class:on={i <= at}></span>
	{/each}
</div>

<style>
	.dots {
		display: flex;
		justify-content: center;
		gap: 8px;
	}
	span {
		width: 12px;
		height: 12px;
		border-radius: 50%;
		border: var(--line);
	}
	.on {
		background: var(--ink);
	}
</style>
```

`web/src/lib/components/TriageList.svelte`:
```svelte
<script lang="ts">
	import type { Action, Review } from '#lib/api.ts';
	import type { Choices } from '#lib/triage.ts';

	interface Props {
		review: Review;
		choices: Choices;
	}
	let { review, choices = $bindable() }: Props = $props();

	const reasons = $derived(new Map(review.suggestions.map((s) => [s.task_id, s.reason])));
	const actions: { id: Action; label: string }[] = [
		{ id: 'roll_over', label: 'Roll over' },
		{ id: 'push', label: 'Push' },
		{ id: 'drop', label: 'Drop' }
	];
</script>

{#if review.fallback}
	<p class="chip rough">rough suggestions, model was out</p>
{/if}
<ul class="triage">
	{#each review.unfinished as task (task.id)}
		<li class="sticker">
			<div class="what">
				<strong>{task.title}</strong>
				{#if reasons.get(task.id)}<span class="muted reason">{reasons.get(task.id)}</span>{/if}
			</div>
			<div class="pick" role="radiogroup" aria-label="What to do with {task.title}">
				{#each actions as a (a.id)}
					<button
						class="chip"
						class:on={choices[task.id].action === a.id}
						role="radio"
						aria-checked={choices[task.id].action === a.id}
						onclick={() => (choices[task.id].action = a.id)}
					>
						{a.label}
					</button>
				{/each}
				{#if choices[task.id].action === 'push'}
					<input class="field date" type="date" bind:value={choices[task.id].until} aria-label="Push until" />
				{/if}
			</div>
		</li>
	{/each}
</ul>

<style>
	.triage {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	li {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
		padding: 10px 14px;
	}
	.what {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.reason {
		font-size: 13px;
		font-style: italic;
	}
	.pick {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
	}
	.chip {
		background: var(--card);
		cursor: pointer;
	}
	.chip.on {
		background: var(--chip);
	}
	.date {
		width: auto;
		padding: 2px 8px;
	}
	.rough {
		align-self: center;
		margin: 0;
	}
</style>
```

`web/src/routes/(ritual)/morning/+page.svelte`:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, paceLabels, type Review, type Tier } from '#lib/api.ts';
	import Dots from '#lib/components/Dots.svelte';
	import TriageList from '#lib/components/TriageList.svelte';
	import { tiers } from '#lib/plan.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { choicesFor, triagePayload, type Choices } from '#lib/triage.ts';
	import { today } from '#lib/today.svelte.ts';

	type Step = 'greet' | 'reconcile' | 'pace' | 'making' | 'reveal';

	let step = $state<Step>('greet');
	let review = $state<Review | null>(null);
	let choices = $state<Choices>({});
	let pace = $state(3);
	let mood = $state('');
	let notice = $state('');

	const reconciling = $derived((review?.unfinished.length ?? 0) > 0);
	const steps = $derived<Step[]>(reconciling ? ['reconcile', 'pace', 'reveal'] : ['pace', 'reveal']);
	const at = $derived(Math.max(0, steps.indexOf(step === 'making' ? 'reveal' : step)));
	const tomorrow = $derived(today.today ? addDays(today.today.date, 1) : '');
	const revealed = $derived(today.today ? tiers(today.today, today.tasks) : null);
	const tierNames: Tier[] = ['must', 'should', 'could'];

	async function loadReview(kept: Choices = {}) {
		review = (await api.morning()).review;
		if (review) choices = choicesFor(review, tomorrow, kept);
	}

	onMount(async () => {
		try {
			await today.refresh();
			// A day that already started has nothing to check in for.
			if (today.today?.plan) return goto('/', { replaceState: true });
			await loadReview();
			step = reconciling ? 'reconcile' : 'pace';
		} catch (err) {
			toasts.error(err);
			step = 'pace';
		}
	});

	async function start() {
		step = 'making';
		notice = '';
		try {
			await api.start(pace, mood.trim(), review ? triagePayload(choices) : []);
			await today.refresh();
			step = 'reveal';
		} catch (err) {
			if (err instanceof ApiError && err.status === 409 && review) {
				// A task on the list changed elsewhere. Show the fresh list, keeping what still applies.
				await loadReview(choices).catch((e) => toasts.error(e));
				notice = "Something changed, here's the fresh list.";
				step = reconciling ? 'reconcile' : 'pace';
			} else {
				toasts.error(err);
				step = 'pace';
			}
		}
	}
</script>

<Dots count={steps.length} {at} />

{#if step === 'greet'}
	<h1>Good morning ☀︎</h1>
	<p class="muted center">Looking at where things stand…</p>
{:else if step === 'reconcile' && review}
	<h1>Good morning ☀︎</h1>
	<p class="center">
		{#if review.done.length}Last time you got {review.done.length} {review.done.length === 1 ? 'thing' : 'things'} done.{/if}
		Here's what's still open. What should happen to each?
	</p>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<TriageList {review} bind:choices />
	<button class="btn primary next" onclick={() => (step = 'pace')}>Next →</button>
{:else if step === 'pace'}
	<h1>How's the tank?</h1>
	<div class="paces">
		{#each paceLabels as label, i (label)}
			<button class="chip" class:on={pace === i + 1} onclick={() => (pace = i + 1)}>{label}</button>
		{/each}
	</div>
	<textarea class="field" rows="3" bind:value={mood} placeholder="Anything on your mind? (optional)"></textarea>
	<div class="row">
		{#if reconciling}<button class="btn" onclick={() => (step = 'reconcile')}>← Back</button>{/if}
		<button class="btn primary" onclick={start}>Plan my day →</button>
	</div>
{:else if step === 'making'}
	<h1>Making your plan…</h1>
	<p class="muted center">This takes a few seconds.</p>
{:else if step === 'reveal' && today.today?.plan && revealed}
	<p class="opening">{today.today.plan.opening || "Here's today."}</p>
	<div class="sticker reveal">
		{#each tierNames as tier (tier)}
			{#if revealed[tier].length}
				<p class="label">{tier}</p>
				<ul>
					{#each revealed[tier] as card (card.task.id)}<li>{card.task.title}</li>{/each}
				</ul>
			{/if}
		{/each}
	</div>
	<button class="btn primary next" onclick={() => goto('/')}>Let's go →</button>
{/if}

<style>
	h1 {
		margin: 0;
		text-align: center;
		font-size: 30px;
		font-weight: var(--heavy);
	}
	.center {
		text-align: center;
		margin: 0;
	}
	.notice {
		align-self: center;
		margin: 0;
	}
	.paces {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: 8px;
	}
	.chip {
		background: var(--card);
		cursor: pointer;
		font-size: 15px;
		padding: 4px 14px;
	}
	.chip.on {
		background: var(--chip);
	}
	.row {
		display: flex;
		justify-content: space-between;
	}
	.row .primary:only-child {
		margin-left: auto;
	}
	.next {
		align-self: flex-end;
	}
	.opening {
		font-size: 22px;
		font-weight: var(--heavy);
		text-align: center;
		margin: 0;
	}
	.reveal {
		padding: 14px 20px;
	}
	.reveal ul {
		margin: 4px 0 12px;
	}
</style>
```

- [ ] **Step 2: Type-check and test**

Run: `npm --prefix web run check && npm --prefix web test`
Expected: `0 ERRORS`, and every test passes.

- [ ] **Step 3: Commit**
```bash
git add web/src
git commit -m "Add the good-morning ritual"
```

---

### Task 5: The wrap-up ritual

**Files:**
- Create: `web/src/routes/(ritual)/wrap/+page.svelte`

**Interfaces:**
- Consumes: `api.wrapReview`, `api.wrap`, `Dots`, `TriageList`, `choicesFor`, `triagePayload`, `winLine`, `survived`, and `live.now`.

- [ ] **Step 1: Write the page**

`web/src/routes/(ritual)/wrap/+page.svelte`:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, type Day, type Review } from '#lib/api.ts';
	import Dots from '#lib/components/Dots.svelte';
	import TriageList from '#lib/components/TriageList.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { winLine } from '#lib/plan.ts';
	import { addDays, survived } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { choicesFor, triagePayload, type Choices } from '#lib/triage.ts';
	import { today } from '#lib/today.svelte.ts';

	type Step = 'loading' | 'done' | 'unfinished' | 'note' | 'goodnight';

	let step = $state<Step>('loading');
	let review = $state<Review | null>(null);
	let choices = $state<Choices>({});
	let note = $state('');
	let notice = $state('');
	let wrapping = $state(false);
	let wrapped = $state<Day | null>(null);

	const unfinished = $derived((review?.unfinished.length ?? 0) > 0);
	const steps = $derived<Step[]>(unfinished ? ['done', 'unfinished', 'note', 'goodnight'] : ['done', 'note', 'goodnight']);
	const at = $derived(Math.max(0, steps.indexOf(step)));
	const tomorrow = $derived(today.today ? addDays(today.today.date, 1) : '');

	async function loadReview(kept: Choices = {}) {
		review = await api.wrapReview();
		choices = choicesFor(review, tomorrow, kept);
	}

	onMount(async () => {
		try {
			await today.refresh();
			const day = today.today?.day;
			// Nothing to wrap before the day starts, or once it's wrapped.
			if (!day || day.wrapped_at) return goto('/', { replaceState: true });
			await loadReview();
			step = 'done';
		} catch (err) {
			toasts.error(err);
		}
	});

	async function wrap() {
		wrapping = true;
		notice = '';
		try {
			wrapped = (await api.wrap(review ? triagePayload(choices) : [], note.trim())).data;
			await today.refresh();
			step = 'goodnight';
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				// A task on the list changed elsewhere. Show the fresh list, keeping what still applies.
				await loadReview(choices).catch((e) => toasts.error(e));
				notice = "Something changed, here's the fresh list.";
				step = unfinished ? 'unfinished' : 'note';
			} else {
				toasts.error(err);
			}
		} finally {
			wrapping = false;
		}
	}
</script>

<Dots count={steps.length} {at} />

{#if step === 'loading'}
	<h1>Wrapping up</h1>
	<p class="muted center">Looking back at today…</p>
{:else if step === 'done' && review}
	<h1>{winLine(review.done.length, survived(today.events, live.now))}</h1>
	{#if review.done.length}
		<ul class="sticker done">
			{#each review.done as task (task.id)}<li>{task.title}</li>{/each}
		</ul>
	{:else}
		<p class="center">Some days are like that. Showing up counts.</p>
	{/if}
	<button class="btn primary next" onclick={() => (step = unfinished ? 'unfinished' : 'note')}>Next →</button>
{:else if step === 'unfinished' && review}
	<h1>What about the rest?</h1>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<TriageList {review} bind:choices />
	<div class="row">
		<button class="btn" onclick={() => (step = 'done')}>← Back</button>
		<button class="btn primary" onclick={() => (step = 'note')}>Next →</button>
	</div>
{:else if step === 'note'}
	<h1>Anything for tomorrow?</h1>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<textarea class="field" rows="4" bind:value={note} placeholder="A note for tomorrow-you (optional)"></textarea>
	<div class="row">
		<button class="btn" onclick={() => (step = unfinished ? 'unfinished' : 'done')}>← Back</button>
		<button class="btn primary" disabled={wrapping} onclick={wrap}>{wrapping ? 'Wrapping up…' : 'Wrap up'}</button>
	</div>
{:else if step === 'goodnight'}
	<h1>Goodnight 🌙</h1>
	{#if wrapped?.summary}<p class="sticker summary">{wrapped.summary}</p>{/if}
	<button class="btn primary next" onclick={() => goto('/')}>Goodnight →</button>
{/if}

<style>
	h1 {
		margin: 0;
		text-align: center;
		font-size: 30px;
		font-weight: var(--heavy);
	}
	.center {
		text-align: center;
		margin: 0;
	}
	.notice {
		align-self: center;
		margin: 0;
	}
	.done {
		margin: 0;
		padding: 14px 20px 14px 36px;
	}
	.done li {
		text-decoration: line-through;
		text-decoration-thickness: 2px;
	}
	.summary {
		margin: 0;
		padding: 14px 20px;
	}
	.row {
		display: flex;
		justify-content: space-between;
	}
	.next {
		align-self: flex-end;
	}
</style>
```

- [ ] **Step 2: Type-check and test**

Run: `npm --prefix web run check && npm --prefix web test`
Expected: `0 ERRORS`, and every test passes.

- [ ] **Step 3: Commit**
```bash
git add web/src
git commit -m "Add the wrap-up ritual"
```

---

### Task 6: All tasks and the edit sheet

**Files:**
- Create: `web/src/lib/components/EditSheet.svelte`, `web/src/lib/components/TaskEditor.svelte`, `web/src/routes/(app)/tasks/+page.svelte`
- Modify: `web/src/lib/components/TaskCard.svelte`, `web/src/routes/(app)/+layout.svelte` (restore `EditSheet`)

**Interfaces:**
- Consumes: `editor`, `formFor`, `fieldsFrom`, `listTasks`, `filters`, `today.blockers`, `api.updateTask`, `api.addBlocker`, and `api.removeBlocker`.
- Produces: `TaskCard {card, tier?, date, listed?}`. `listed` shows the date badges, and a click opens the editor instead of the rationale.

- [ ] **Step 1: Write the components and page**

Replace `web/src/lib/components/TaskCard.svelte` with:
```svelte
<script lang="ts">
	import { api, type Tier } from '#lib/api.ts';
	import { editor } from '#lib/editor.svelte.ts';
	import type { Card } from '#lib/plan.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		card: Card;
		/** Absent for an unplanned card. */
		tier?: Tier;
		/** Today's date, for push to tomorrow. */
		date: string;
		/** All tasks shows dates, and opens the editor on a click instead of the rationale. */
		listed?: boolean;
	}
	let { card, tier, date, listed = false }: Props = $props();

	let menuOpen = $state(false);
	let why = $state(false);
	const task = $derived(card.task);

	// The card leaves once the refetch lands. If the write failed, untick it again.
	async function complete(e: Event & { currentTarget: HTMLInputElement }) {
		const box = e.currentTarget;
		if (!(await toasts.track(`Done: ${task.title}`, () => api.setStatus(task.id, 'done')))) {
			box.checked = false;
		}
	}
	function push() {
		menuOpen = false;
		toasts.track(`Pushed to tomorrow: ${task.title}`, () => api.push(task.id, addDays(date, 1)));
	}
	function drop() {
		menuOpen = false;
		toasts.track(`Dropped: ${task.title}`, () => api.setStatus(task.id, 'dropped'));
	}
	function edit() {
		menuOpen = false;
		editor.open(task.id);
	}
	function restore() {
		toasts.track(`Restored: ${task.title}`, () => api.setStatus(task.id, 'open'));
	}
	const short = (date: string) =>
		new Date(`${date}T12:00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
</script>

<article class="sticker card {tier ?? (listed ? 'listed' : 'unplanned')}" class:closed={task.status !== 'open'}>
	{#if task.status === 'open'}
		<input type="checkbox" aria-label="Mark done" onchange={complete} />
	{:else}
		<span class="mark" aria-label={task.status}>{task.status === 'done' ? '✓' : '✕'}</span>
	{/if}
	<button
		class="body"
		onclick={() => (listed ? editor.open(task.id) : (why = !why))}
		disabled={!listed && !card.rationale}
	>
		<span class="title">
			{#if card.blocked}<span title="Blocked by another task">⛓</span>{/if}
			{task.title}
		</span>
		<span class="badges">
			{#if task.size}<span class="badge">{task.size}</span>{/if}
			{#if task.energy}<span class="badge">{task.energy} energy</span>{/if}
			{#if listed && task.due_on}<span class="badge">due {short(task.due_on)}</span>{/if}
			{#if listed && task.not_before}<span class="badge">from {short(task.not_before)}</span>{/if}
		</span>
		{#if task.feeling}<span class="feeling">"{task.feeling}"</span>{/if}
		{#if card.rationale}<span class="why" class:shown={why}>{card.rationale}</span>{/if}
	</button>
	{#if task.status === 'dropped'}
		<button class="btn" onclick={restore}>Restore</button>
	{:else if task.status === 'open'}
		<details class="more" bind:open={menuOpen}>
			<summary aria-label="More actions">⋯</summary>
			<div class="sticker menu">
				<button onclick={edit}>Edit</button>
				<button onclick={push}>Push to tomorrow</button>
				<button onclick={drop}>Drop</button>
			</div>
		</details>
	{/if}
</article>

<style>
	.card {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 10px 12px;
		margin-bottom: 10px;
	}
	.must {
		background: var(--must);
	}
	.should {
		background: var(--should);
	}
	.could {
		background: var(--could);
	}
	.unplanned {
		border-style: dashed;
		box-shadow: none;
	}
	.closed {
		opacity: 0.7;
	}
	.mark {
		width: 20px;
		text-align: center;
		font-weight: var(--heavy);
	}
	input[type='checkbox'] {
		width: 20px;
		height: 20px;
		margin-top: 2px;
		accent-color: var(--primary);
	}
	.body {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 3px;
		background: none;
		border: none;
		padding: 0;
		text-align: left;
		cursor: default;
	}
	.body:not(:disabled) {
		cursor: pointer;
	}
	.title {
		font-weight: var(--heavy);
	}
	.badges {
		display: flex;
		gap: 6px;
	}
	.badge {
		font-size: 11px;
		font-weight: var(--heavy);
		border: 1.5px solid var(--ink);
		border-radius: var(--pill);
		padding: 0 7px;
		background: var(--card);
	}
	.feeling {
		font-style: italic;
		font-size: 13px;
		color: var(--ink-soft);
	}
	.why {
		display: none;
		font-size: 13px;
	}
	.why.shown,
	.card:hover .why {
		display: block;
	}
	.more {
		position: relative;
	}
	.more summary {
		list-style: none;
		cursor: pointer;
		font-weight: var(--heavy);
		padding: 0 4px;
	}
	.menu {
		position: absolute;
		right: 0;
		top: 24px;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 160px;
	}
	.menu button {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.menu button:hover {
		background: var(--chip);
	}
</style>
```

`web/src/lib/components/EditSheet.svelte`:
```svelte
<script lang="ts">
	import { editor } from '#lib/editor.svelte.ts';
	import { today } from '#lib/today.svelte.ts';
	import TaskEditor from './TaskEditor.svelte';

	const task = $derived(today.tasks.find((t) => t.id === editor.taskId));
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && editor.close()} />

{#if task}
	<div class="scrim" role="presentation" onclick={() => editor.close()}></div>
	<div class="sticker sheet" role="dialog" aria-modal="true" aria-label="Edit task">
		{#key task.id}
			<TaskEditor {task} />
		{/key}
	</div>
{/if}

<style>
	.scrim {
		position: fixed;
		inset: 0;
		background: color-mix(in srgb, var(--ink) 40%, transparent);
		z-index: 40;
	}
	.sheet {
		position: fixed;
		top: 5vh;
		left: 50%;
		transform: translateX(-50%);
		width: min(560px, calc(100vw - 32px));
		max-height: 90vh;
		overflow-y: auto;
		z-index: 41;
		background: var(--paper);
		padding: 18px 20px;
	}
</style>
```

`web/src/lib/components/TaskEditor.svelte`:
```svelte
<script lang="ts">
	import { api, type Task } from '#lib/api.ts';
	import { editor } from '#lib/editor.svelte.ts';
	import { fieldsFrom, formFor } from '#lib/taskForm.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';

	interface Props {
		task: Task;
	}
	let { task }: Props = $props();

	// Seeded once: the sheet is keyed by task, so a refetch mid-edit doesn't wipe what's typed.
	// svelte-ignore state_referenced_locally
	let form = $state(formFor(task));
	let problem = $state('');
	let saving = $state(false);
	let adding = $state('');

	const titleOf = (id: number) => today.tasks.find((t) => t.id === id)?.title ?? `#${id}`;
	const blockedBy = $derived(today.blockers.filter((b) => b.task_id === task.id).map((b) => b.blocked_by_id));
	const candidates = $derived(
		today.tasks.filter((t) => t.status === 'open' && t.id !== task.id && !blockedBy.includes(t.id))
	);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		const result = fieldsFrom(form, task);
		if ('error' in result) {
			problem = result.error;
			return;
		}
		saving = true;
		if (await toasts.track(`Saved: ${result.fields.title}`, () => api.updateTask(task.id, result.fields))) {
			editor.close();
		}
		saving = false;
	}

	function addBlocker() {
		const id = Number(adding);
		adding = '';
		if (id) toasts.track(`Now waits on: ${titleOf(id)}`, () => api.addBlocker(task.id, id));
	}

	function removeBlocker(id: number) {
		toasts.track(`No longer waits on: ${titleOf(id)}`, () => api.removeBlocker(task.id, id));
	}
</script>

<form onsubmit={save}>
	<header>
		<h2>Edit task</h2>
		<button type="button" class="close" aria-label="Close" onclick={() => editor.close()}>✕</button>
	</header>
	<label>
		<span class="label">Title</span>
		<input class="field" bind:value={form.title} />
	</label>
	{#if problem}<p class="problem">{problem}</p>{/if}
	<label>
		<span class="label">Notes</span>
		<textarea class="field" rows="3" bind:value={form.notes}></textarea>
	</label>
	<div class="row">
		<label>
			<span class="label">Size</span>
			<select class="field" bind:value={form.size}>
				<option value="">none</option>
				<option value="S">S</option>
				<option value="M">M</option>
				<option value="L">L</option>
			</select>
		</label>
		<label>
			<span class="label">Energy</span>
			<select class="field" bind:value={form.energy}>
				<option value="">none</option>
				<option value="low">low</option>
				<option value="med">med</option>
				<option value="high">high</option>
			</select>
		</label>
	</div>
	<label>
		<span class="label">Feeling</span>
		<input class="field" bind:value={form.feeling} placeholder="How does this one feel?" />
	</label>
	<div class="row">
		<label>
			<span class="label">Due</span>
			<input class="field" type="date" bind:value={form.due_on} />
		</label>
		<label>
			<span class="label">Not before</span>
			<input class="field" type="date" bind:value={form.not_before} />
		</label>
	</div>

	<section>
		<span class="label">Blocked by</span>
		<ul>
			{#each blockedBy as id (id)}
				<li>
					{titleOf(id)}
					<button type="button" class="close" aria-label="Remove blocker {titleOf(id)}" onclick={() => removeBlocker(id)}>✕</button>
				</li>
			{:else}
				<li class="muted">Nothing. It's free to start.</li>
			{/each}
		</ul>
		{#if candidates.length}
			<select class="field" bind:value={adding} onchange={addBlocker} aria-label="Add a blocker">
				<option value="">Add a blocker…</option>
				{#each candidates as t (t.id)}
					<option value={String(t.id)}>{t.title}</option>
				{/each}
			</select>
		{/if}
	</section>

	{#if task.raw_text}
		<section>
			<span class="label">As captured</span>
			<p class="raw">{task.raw_text}</p>
		</section>
	{/if}

	<div class="actions">
		<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
		<button type="button" class="btn" onclick={() => editor.close()}>Cancel</button>
	</div>
</form>

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	h2 {
		margin: 0;
		font-weight: var(--heavy);
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
		flex: 1;
	}
	.row {
		display: flex;
		gap: 12px;
	}
	ul {
		margin: 6px 0;
		padding-left: 18px;
	}
	.close {
		background: none;
		border: none;
	}
	.raw {
		margin: 4px 0 0;
		white-space: pre-wrap;
		font-size: 14px;
		color: var(--ink-soft);
	}
	.problem {
		margin: 0;
		color: var(--danger);
		font-weight: var(--heavy);
	}
	.actions {
		display: flex;
		gap: 10px;
	}
</style>
```

`web/src/routes/(app)/tasks/+page.svelte`:
```svelte
<script lang="ts">
	import Header from '#lib/components/Header.svelte';
	import TaskCard from '#lib/components/TaskCard.svelte';
	import { filters, listTasks, type Filter } from '#lib/tasks.ts';
	import { today } from '#lib/today.svelte.ts';

	let filter = $state<Filter>('open');

	const t = $derived(today.today);
	const blocked = $derived(new Set(t?.blocked ?? []));
	const shown = $derived(t ? listTasks(today.tasks, today.blockers, filter, t.date) : []);
</script>

{#if t}
	<Header today={t} />
	<div class="chips" role="tablist" aria-label="Filter tasks">
		{#each filters as f (f.id)}
			<button
				class="chip"
				class:on={filter === f.id}
				role="tab"
				aria-selected={filter === f.id}
				onclick={() => (filter = f.id)}
			>
				{f.label}
			</button>
		{/each}
	</div>
	<section>
		{#each shown as task (task.id)}
			<TaskCard card={{ task, rationale: '', blocked: blocked.has(task.id) }} date={t.date} listed />
		{:else}
			<p class="muted">Nothing here.</p>
		{/each}
	</section>
{:else}
	<p class="muted">Loading…</p>
{/if}

<style>
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}
	.chip {
		background: var(--card);
		cursor: pointer;
	}
	.chip.on {
		background: var(--chip);
	}
</style>
```

Restore the `EditSheet` import and `<EditSheet />` tag in `web/src/routes/(app)/+layout.svelte`, to match the version in Task 3.

- [ ] **Step 2: Type-check and test**

Run: `npm --prefix web run check && npm --prefix web test`
Expected: `0 ERRORS 0 WARNINGS`, and every test passes.

- [ ] **Step 3: Commit**
```bash
git add web/src
git commit -m "Add All tasks and the edit sheet"
```

---

### Task 7: Smoke test the real binary

There is nothing to commit unless a step finds a bug. If one does, fix it with a test where the fix lives in a logic module.

- [ ] **Step 1: Build and run against a throwaway database** on port 7788. The sandbox blocks localhost, so the curl commands need it disabled.
```bash
just build
./whatnow serve -addr 127.0.0.1:7788 -db "$TMPDIR/whatnow-5b.db"
```

- [ ] **Step 2: Seed the data** as in part 1's smoke test: four tasks (one due today), a blocker from 2 on 4, three events, and `POST /api/v1/today/start`. Then backdate that day, so the next load is a morning with yesterday left unwrapped:
```bash
sqlite3 "$TMPDIR/whatnow-5b.db" "UPDATE days SET date='$(date -v-1d +%F)';"
```

- [ ] **Step 3: Check each behavior at http://127.0.0.1:7788**
  - **Into the morning:** `/` redirects to `/morning`. The greeting shows, then the triage list, with "rough suggestions" because no model is set up.
  - **Triage:** pick Drop on one task and Push on another, and the date field appears prefilled with tomorrow. Next, pick a pace, then **Plan my day**: "Making your plan…", then the opening line and the tier lists. **Let's go** opens Today. The dropped task is gone and the pushed one is out of the tiers.
  - **Toasts:** a toast's faint fill sweeps left to right. Hovering freezes it, and leaving resumes it.
  - **Edit:** ⋯ → Edit on a card opens the sheet. Change the title, add a blocker, and **Save**: the sheet closes, the card shows the new title and ⛓, and Undo toasts appear. Escape closes the sheet without saving.
  - **All tasks** (⋯ → All tasks): each chip filters correctly, the date badges show, and Restore on a dropped task moves it back to Open.
  - **Wrap-up** (⋯ → Wrap up early): the done step, then triage, then a note, then **Wrap up**, then Goodnight. Today shows the Day's-done card with the note. What now? and Wrap up are hidden.
  - **Redirects out:** visiting `/wrap` and then `/morning` both redirect to Today.
  - **A 409 mid-ritual:** backdate and clear the wrap again (`UPDATE days SET date='<yesterday>', wrapped_at=NULL;`) and open `/morning`. While the triage list shows, complete one of its tasks via the API (`POST /api/v1/tasks/<id>/status {"status":"done"}`), then plan the day. The page should show "Something changed, here's the fresh list" without that task.
  - **Midnight catch-up:** with Today open, move today's day row to an older date in SQLite (`UPDATE days SET date='<two days ago>' WHERE date=date('now','localtime')`), switch to another window, then back. The focus refetch fires and the tab goes to `/morning`. The server's date can't be faked, so the date-change rule itself rests on the `morningDue` unit test.
  - **Wrap button timing:** set the end of day to within an hour of now via `PUT /api/v1/settings`, round-tripping the full object. Reload, and **Wrap up** appears in the header.

- [ ] **Step 4: Run every check**

Run: `gofmt -l . && just lint && just test`
Expected: no gofmt output, `0 ERRORS`, every Go package `ok`, and every Vitest test passing.
