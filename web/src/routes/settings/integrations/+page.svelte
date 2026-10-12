<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Integration } from '#lib/api.ts';
	import IntegrationCard from '#lib/components/IntegrationCard.svelte';
	import { toasts } from '#lib/toasts.svelte.ts';

	let list = $state<Integration[] | null>(null);

	onMount(() => {
		api.integrations().then(
			(l) => (list = l),
			(err) => toasts.error(err)
		);
	});
</script>

{#if list}
	{#each list as integration, i (integration.id)}
		<IntegrationCard bind:integration={list[i]} />
	{/each}
{:else}
	<p class="muted">Loading…</p>
{/if}
