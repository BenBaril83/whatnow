<script lang="ts">
	import { kofiUrl, sourceUrl } from '#lib/links.ts';

	interface Props {
		/** Only the hosted server has a bill for tips to cover. */
		hosted: boolean;
	}
	let { hosted }: Props = $props();
</script>

<section class="sticker">
	<h2 class="label">{hosted ? 'Support whatnow' : 'Open source'}</h2>
	<p>
		{#if hosted}
			whatnow is free while you bring your own key. Tips go straight to the server bill.
		{:else}
			whatnow is free, and its code is on GitHub.
		{/if}
	</p>
	<div class="links">
		{#if hosted}
			<a class="btn" href={kofiUrl} target="_blank" rel="noopener noreferrer">Chip in on Ko-fi</a>
		{/if}
		<a class="btn" href={sourceUrl} target="_blank" rel="noopener noreferrer">Source on GitHub</a>
	</div>
</section>

<style>
	section {
		padding: 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	h2,
	p {
		margin: 0;
	}
	.links {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}
	a {
		text-decoration: none;
	}
</style>
