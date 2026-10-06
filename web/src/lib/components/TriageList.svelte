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
