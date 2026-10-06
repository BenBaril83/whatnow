// Types mirror the Go JSON. Only the fields the client reads are listed.

export type Size = 'S' | 'M' | 'L';
export type Energy = 'low' | 'med' | 'high';
export type Status = 'open' | 'done' | 'dropped';
export type Tier = 'must' | 'should' | 'could';
export type Source = 'user' | 'chat' | 'cleanup' | 'triage';
export type Provider = 'anthropic' | 'openai';
/** Where a provider's key comes from. Empty means there is none. */
export type KeySource = '' | 'keychain' | 'env';

export interface Task {
	id: number;
	title: string;
	raw_text: string;
	size: Size | null;
	energy: Energy | null;
	feeling: string;
	/** A push sets this. The task is put off until then. */
	not_before: string | null;
	status: Status;
	updated_at: string;
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
	start: (pace: number, mood_note: string) =>
		request<unknown>('POST', '/today/start', { pace, mood_note, triage: [] }),
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
