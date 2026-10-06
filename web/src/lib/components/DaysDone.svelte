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
