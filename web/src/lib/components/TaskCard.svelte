<script lang="ts">
	import { api, type Tier } from '#lib/api.ts';
	import type { Card } from '#lib/plan.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		card: Card;
		/** Absent for an unplanned card. */
		tier?: Tier;
		/** Today's date, for push to tomorrow. */
		date: string;
	}
	let { card, tier, date }: Props = $props();

	let menuOpen = $state(false);
	let why = $state(false);
	const task = $derived(card.task);

	// The card leaves once the refetch lands. If the write failed, untick it again.
	async function complete(e: Event & { currentTarget: HTMLInputElement }) {
		const box = e.currentTarget;
		if (!(await toasts.track(`Done: ${task.title}`, () => api.setStatus(task.id, 'done')))) {
			box.checked = false;
		}
	}
	function push() {
		menuOpen = false;
		toasts.track(`Pushed to tomorrow: ${task.title}`, () => api.push(task.id, addDays(date, 1)));
	}
	function drop() {
		menuOpen = false;
		toasts.track(`Dropped: ${task.title}`, () => api.setStatus(task.id, 'dropped'));
	}
</script>

<article class="sticker card {tier ?? 'unplanned'}">
	<input type="checkbox" aria-label="Mark done" onchange={complete} />
	<button class="body" onclick={() => (why = !why)} disabled={!card.rationale}>
		<span class="title">
			{#if card.blocked}<span title="Blocked by another task">⛓</span>{/if}
			{task.title}
		</span>
		<span class="badges">
			{#if task.size}<span class="badge">{task.size}</span>{/if}
			{#if task.energy}<span class="badge">{task.energy} energy</span>{/if}
		</span>
		{#if task.feeling}<span class="feeling">"{task.feeling}"</span>{/if}
		{#if card.rationale}<span class="why" class:shown={why}>{card.rationale}</span>{/if}
	</button>
	<details class="more" bind:open={menuOpen}>
		<summary aria-label="More actions">⋯</summary>
		<div class="sticker menu">
			<button onclick={push}>Push to tomorrow</button>
			<button onclick={drop}>Drop</button>
		</div>
	</details>
</article>

<style>
	.card {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 10px 12px;
		margin-bottom: 10px;
	}
	.must {
		background: var(--must);
	}
	.should {
		background: var(--should);
	}
	.could {
		background: var(--could);
	}
	.unplanned {
		border-style: dashed;
		box-shadow: none;
	}
	input[type='checkbox'] {
		width: 20px;
		height: 20px;
		margin-top: 2px;
		accent-color: var(--primary);
	}
	.body {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 3px;
		background: none;
		border: none;
		padding: 0;
		text-align: left;
		cursor: default;
	}
	.body:not(:disabled) {
		cursor: pointer;
	}
	.title {
		font-weight: var(--heavy);
	}
	.badges {
		display: flex;
		gap: 6px;
	}
	.badge {
		font-size: 11px;
		font-weight: var(--heavy);
		border: 1.5px solid var(--ink);
		border-radius: var(--pill);
		padding: 0 7px;
		background: var(--card);
	}
	.feeling {
		font-style: italic;
		font-size: 13px;
		color: var(--ink-soft);
	}
	.why {
		display: none;
		font-size: 13px;
	}
	.why.shown,
	.card:hover .why {
		display: block;
	}
	.more {
		position: relative;
	}
	.more summary {
		list-style: none;
		cursor: pointer;
		font-weight: var(--heavy);
		padding: 0 4px;
	}
	.menu {
		position: absolute;
		right: 0;
		top: 24px;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 160px;
	}
	.menu button {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.menu button:hover {
		background: var(--chip);
	}
</style>
