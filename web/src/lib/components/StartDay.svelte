<script lang="ts">
	import { api, paceLabels } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';

	let pace = $state(3);
	let mood = $state('');
	let starting = $state(false);

	// The good-morning flow replaces this card.
	async function start(e: SubmitEvent) {
		e.preventDefault();
		starting = true;
		try {
			await api.start(pace, mood.trim());
			await today.refresh();
		} catch (err) {
			toasts.error(err);
		} finally {
			starting = false;
		}
	}
</script>

<form class="sticker start" onsubmit={start}>
	<h2>Start my day</h2>
	<p class="label">Pace</p>
	<div class="paces">
		{#each paceLabels as label, i (label)}
			<button type="button" class="chip" class:picked={pace === i + 1} onclick={() => (pace = i + 1)}>
				{label}
			</button>
		{/each}
	</div>
	<textarea class="field" bind:value={mood} rows="2" placeholder="Anything on your mind? (optional)"></textarea>
	<button class="btn primary" disabled={starting}>{starting ? 'Planning…' : 'Plan my day'}</button>
</form>

<style>
	.start {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 16px 20px;
		align-items: flex-start;
	}
	h2 {
		margin: 0;
		font-weight: var(--heavy);
	}
	.paces {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}
	.chip {
		background: var(--card);
	}
	.chip.picked {
		background: var(--chip);
	}
</style>
