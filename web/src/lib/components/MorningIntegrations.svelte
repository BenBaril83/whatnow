<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Integration, type PullResult } from '#lib/api.ts';
	import { isOn } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import PullResults from './PullResults.svelte';

	/** The integrations that can pull. With none, the row doesn't show. */
	let on = $state<Integration[]>([]);
	let results = $state<Record<string, PullResult>>({});
	let pulling = $state<Record<string, boolean>>({});

	onMount(() => {
		api.integrations().then(
			(all) => (on = all.filter(isOn)),
			(err) => toasts.error(err)
		);
	});

	async function pull(id: string) {
		pulling[id] = true;
		try {
			results[id] = await api.pull(id);
		} catch (err) {
			toasts.error(err);
		} finally {
			pulling[id] = false;
		}
	}
</script>

{#if on.length}
	<section class="integrations" aria-label="Integrations">
		<h2 class="label">Integrations</h2>
		{#each on as i (i.id)}
			<button class="chip" disabled={pulling[i.id]} onclick={() => pull(i.id)}>
				{pulling[i.id] ? 'Pulling…' : `↻ ${i.name}`}
			</button>
			{#if results[i.id]}
				<PullResults id={i.id} bind:result={results[i.id]} retry={() => pull(i.id)} />
			{/if}
		{/each}
	</section>
{/if}

<style>
	.integrations {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 8px;
	}
	h2 {
		margin: 0;
	}
	.integrations :global(.sources) {
		width: 100%;
	}
	.chip {
		background: var(--card);
		cursor: pointer;
		font-size: 15px;
		padding: 4px 14px;
	}
</style>
