<script lang="ts">
	import type { Day } from '#lib/api.ts';
	import { kofiUrl, sourceUrl } from '#lib/links.ts';
	import { winLine } from '#lib/plan.ts';
	import { session } from '#lib/session.svelte.ts';

	interface Props {
		day: Day;
		done: number;
		meetings: number;
	}
	let { day, done, meetings }: Props = $props();
	const hosted = $derived(session.value?.hosted ?? false);
</script>

<section class="sticker daysdone">
	<p class="label">Day's done</p>
	<h2>{winLine(done, meetings)}</h2>
	{#if day.reflection}<p>{day.reflection}</p>{/if}
	{#if day.tomorrow_note}
		<p class="note"><span class="label">For tomorrow</span> {day.tomorrow_note}</p>
	{/if}
	<p class="muted">You're off the hook. Anything you capture now waits in the tray for the morning.</p>
	<p class="muted small">
		whatnow is free and open source ·
		<a href={sourceUrl} target="_blank" rel="noopener noreferrer">source on GitHub</a>
		{#if hosted}
			· <a href={kofiUrl} target="_blank" rel="noopener noreferrer">chip in on Ko-fi ☕</a>
		{/if}
	</p>
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
	.small {
		margin-bottom: 0;
		font-size: 13px;
	}
	.note {
		background: var(--card);
		border: var(--line);
		border-radius: var(--radius-sm);
		padding: 8px 10px;
	}
</style>
