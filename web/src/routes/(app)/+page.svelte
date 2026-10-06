<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, type Pick, type Tier } from '#lib/api.ts';
	import { live } from '#lib/live.svelte.ts';
	import { doneOn, tiers, unplanned } from '#lib/plan.ts';
	import { survived } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';
	import { welcomeDue } from '#lib/welcome.ts';
	import Capture from '#lib/components/Capture.svelte';
	import DayStrip from '#lib/components/DayStrip.svelte';
	import DaysDone from '#lib/components/DaysDone.svelte';
	import DoneToday from '#lib/components/DoneToday.svelte';
	import Header from '#lib/components/Header.svelte';
	import TaskCard from '#lib/components/TaskCard.svelte';
	import WhatNow from '#lib/components/WhatNow.svelte';

	let pick = $state<Pick | null>(null);
	let asking = $state(false);
	/** What now found nothing open and eligible. */
	let nothing = $state(false);

	const t = $derived(today.today);
	const planned = $derived(t ? tiers(t, today.tasks) : null);
	const tray = $derived(t ? unplanned(t, today.tasks) : []);
	const done = $derived(t ? doneOn(t.date, today.tasks) : []);
	const pickTask = $derived(pick ? today.tasks.find((x) => x.id === pick?.task_id) : undefined);
	const tierNames: Tier[] = ['must', 'should', 'could'];

	// A day with no plan starts with the morning check-in, once first-run setup has had its turn.
	$effect(() => {
		if (t && !t.plan && live.view && !welcomeDue(live.view.settings, '/')) goto('/morning', { replaceState: true });
	});

	// A nudge's What now? arrives as ?whatnow, from any page.
	$effect(() => {
		if (t?.plan && page.url.searchParams.has('whatnow')) {
			goto('/', { replaceState: true });
			if (!t.day?.wrapped_at) whatNow();
		}
	});

	async function whatNow(exclude = 0, reason = '') {
		asking = true;
		nothing = false;
		try {
			pick = await api.whatNow(exclude, reason);
		} catch (err) {
			pick = null;
			// 404 means nothing is open and eligible: news to say gently, not an error.
			if (err instanceof ApiError && err.status === 404) nothing = true;
			else toasts.error(err);
		} finally {
			asking = false;
		}
	}
</script>

{#if t?.plan}
	<Header today={t} {asking} onwhatnow={() => whatNow()} />
	<DayStrip events={today.events} now={live.now} endOfDay={live.endOfDay} startedAt={t.day?.started_at ?? null} />
	<Capture />

	{#if pick}
		<WhatNow
			{pick}
			task={pickTask}
			{asking}
			onnotthis={(reason) => whatNow(pick?.task_id, reason)}
			onclose={() => (pick = null)}
		/>
	{:else if nothing}
		<p class="sticker nothing">Nothing to do right now. Enjoy it.</p>
	{/if}

	{#if t.day?.wrapped_at}
		<DaysDone day={t.day} done={done.length} meetings={survived(today.events, live.now)} />
	{:else if planned}
		{#if t.plan.fallback}
			<p class="chip rough">rough plan, model was out</p>
		{/if}
		{#each tierNames as tier (tier)}
			<section>
				<h2 class="label">{tier}</h2>
				{#each planned[tier] as card (card.task.id)}
					<TaskCard {card} {tier} date={t.date} />
				{:else}
					<p class="muted empty">Nothing here.</p>
				{/each}
			</section>
		{/each}
	{/if}

	<div class="lower">
		<section>
			<h2 class="label">Unplanned</h2>
			{#each tray as card (card.task.id)}
				<TaskCard {card} date={t.date} />
			{:else}
				<p class="muted empty">Tray's empty.</p>
			{/each}
		</section>
		<section>
			<DoneToday {done} meetings={survived(today.events, live.now)} />
		</section>
	</div>
{:else}
	<p class="muted">Loading…</p>
{/if}

<style>
	h2 {
		margin: 6px 0 8px;
	}
	.empty {
		margin: 0 0 8px;
		font-size: 14px;
	}
	.nothing {
		margin: 0;
		padding: 12px 16px;
		font-weight: var(--heavy);
	}
	.rough {
		align-self: flex-start;
		margin: 0;
	}
	.lower {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 16px;
		align-items: start;
	}

	@media (max-width: 800px) {
		.lower {
			grid-template-columns: 1fr;
		}
	}
</style>
