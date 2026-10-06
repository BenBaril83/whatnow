<script lang="ts">
	import { api, paceLabels, type CalendarEvent, type Today } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { countdown } from '#lib/time.ts';

	interface Props {
		today: Today;
		events: CalendarEvent[];
		now: Date;
		connected: boolean;
		/** False when no model is set up. */
		configured: boolean;
		asking: boolean;
		onwhatnow: () => void;
	}
	let { today, events, now, connected, configured, asking, onwhatnow }: Props = $props();

	let paceOpen = $state(false);
	const next = $derived(countdown(events, now));
	const date = $derived(
		new Date(`${today.date}T12:00:00`).toLocaleDateString(undefined, {
			weekday: 'long',
			month: 'short',
			day: 'numeric'
		})
	);

	function setPace(pace: number) {
		paceOpen = false;
		toasts.track(`Pace: ${paceLabels[pace - 1]}`, () => api.setPace(pace));
	}
</script>

<header class="sticker">
	<div class="left">
		<h1>{date}</h1>
		{#if today.day}
			<details class="pace" bind:open={paceOpen}>
				<summary class="chip">pace: {paceLabels[today.day.pace - 1]}</summary>
				<div class="sticker menu">
					{#each paceLabels as label, i (label)}
						<button class="item" class:current={i + 1 === today.day.pace} onclick={() => setPace(i + 1)}>
							{label}
						</button>
					{/each}
				</div>
			</details>
		{/if}
		{#if !connected}
			<span class="chip offline">reconnecting…</span>
		{/if}
	</div>
	<div class="right">
		{#if next}
			<span class="next">{next}</span>
		{/if}
		<a class="muted" href="/settings">Settings</a>
		<button class="btn primary" disabled={asking || !today.day} onclick={onwhatnow}>
			{asking ? 'Thinking…' : 'What now?'}
		</button>
	</div>
	{#if !configured}
		<p class="notice">
			No model set up yet. Capture works and plans come out rough. <a href="/settings">Set one up</a>
		</p>
	{/if}
</header>

<style>
	header {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		padding: 12px 16px;
	}
	.left,
	.right {
		display: flex;
		align-items: center;
		gap: 12px;
	}
	h1 {
		margin: 0;
		font-size: 22px;
		font-weight: var(--heavy);
	}
	.next {
		font-weight: var(--heavy);
	}
	.pace {
		position: relative;
	}
	.pace summary {
		list-style: none;
		cursor: pointer;
	}
	.menu {
		position: absolute;
		top: 32px;
		left: 0;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 170px;
	}
	.item {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.item:hover,
	.item.current {
		background: var(--chip);
	}
	.offline {
		background: var(--must);
	}
	.notice {
		flex-basis: 100%;
		margin: 0;
		font-size: 14px;
	}
</style>
