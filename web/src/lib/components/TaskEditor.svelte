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
