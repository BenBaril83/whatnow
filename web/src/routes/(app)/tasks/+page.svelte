<script lang="ts">
	import Header from '#lib/components/Header.svelte';
	import TaskCard from '#lib/components/TaskCard.svelte';
	import { editor } from '#lib/editor.svelte.ts';
	import { filters, listTasks, type Filter } from '#lib/tasks.ts';
	import { today } from '#lib/today.svelte.ts';

	let filter = $state<Filter>('open');

	const t = $derived(today.today);
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
		<button class="btn add" onclick={() => editor.openNew()}>+ Task</button>
	</div>
	<section>
		{#each shown as task (task.id)}
			<TaskCard card={{ task, rationale: '' }} date={t.date} listed />
		{:else}
			<p class="muted">Nothing here.</p>
		{/each}
	</section>
{:else}
	<p class="muted">Loading…</p>
{/if}

<style>
	.add {
		margin-left: auto;
	}
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
