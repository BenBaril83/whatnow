<script lang="ts">
	import { api, configured, paceLabels, type Today } from '#lib/api.ts';
	import { live } from '#lib/live.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { countdown, wrapDue } from '#lib/time.ts';
	import { today as todayState } from '#lib/today.svelte.ts';

	interface Props {
		today: Today;
		/** Today passes these to offer What now?; other pages leave them out. */
		asking?: boolean;
		onwhatnow?: () => void;
	}
	let { today, asking = false, onwhatnow }: Props = $props();

	let paceOpen = $state(false);
	let menuOpen = $state(false);
	const next = $derived(countdown(todayState.events, live.now));
	const wrapped = $derived(today.day?.wrapped_at != null);
	const canWrap = $derived(today.day !== null && !wrapped);
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
		{#if !live.connected}
			<span class="chip offline">reconnecting…</span>
		{/if}
	</div>
	<div class="right">
		{#if next}
			<span class="next">{next}</span>
		{/if}
		{#if canWrap && wrapDue(live.now, live.endOfDay)}
			<a class="btn" href="/wrap">Wrap up</a>
		{/if}
		{#if onwhatnow && !wrapped}
			<button class="btn primary" disabled={asking || !today.day} onclick={onwhatnow}>
				{asking ? 'Thinking…' : 'What now?'}
			</button>
		{/if}
		<details class="more" bind:open={menuOpen}>
			<summary class="btn" aria-label="Menu">⋯</summary>
			<nav class="sticker menu">
				<a class="item" href="/" onclick={() => (menuOpen = false)}>Today</a>
				<a class="item" href="/tasks" onclick={() => (menuOpen = false)}>All tasks</a>
				{#if canWrap}<a class="item" href="/wrap" onclick={() => (menuOpen = false)}>Wrap up early</a>{/if}
				<a class="item" href="/settings" onclick={() => (menuOpen = false)}>Settings</a>
			</nav>
		</details>
	</div>
	{#if live.view && !configured(live.view)}
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
	a.btn,
	a.item {
		color: var(--ink);
		text-decoration: none;
	}
	.more {
		position: relative;
	}
	.more summary {
		list-style: none;
	}
	.more .menu {
		left: auto;
		right: 0;
		top: 40px;
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
