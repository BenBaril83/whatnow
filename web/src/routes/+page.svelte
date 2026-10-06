<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, configured, type Pick, type SettingsView, type Tier } from '#lib/api.ts';
	import { chat } from '#lib/chat.svelte.ts';
	import { doneOn, tiers, unplanned } from '#lib/plan.ts';
	import { Stream } from '#lib/stream.svelte.ts';
	import { survived } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';
	import Capture from '#lib/components/Capture.svelte';
	import ChatDock from '#lib/components/ChatDock.svelte';
	import DayStrip from '#lib/components/DayStrip.svelte';
	import DoneToday from '#lib/components/DoneToday.svelte';
	import Header from '#lib/components/Header.svelte';
	import StartDay from '#lib/components/StartDay.svelte';
	import TaskCard from '#lib/components/TaskCard.svelte';
	import WhatNow from '#lib/components/WhatNow.svelte';

	let now = $state(new Date());
	let view = $state<SettingsView | null>(null);
	let pick = $state<Pick | null>(null);
	let asking = $state(false);
	/** What now found nothing open and eligible. */
	let nothing = $state(false);

	const stream = new Stream(
		{
			'batch.applied': (d) => {
				today.changed();
				toasts.applied(d);
			},
			'batch.undone': (d) => {
				today.changed();
				toasts.undone(d.batch_id);
				chat.markUndone(d.batch_id);
			},
			'plan.updated': () => today.changed(),
			'chat.delta': (d) => chat.delta(d.text),
			'chat.tool': (d) => chat.tool(d),
			'chat.message': (d) => chat.message(d)
		},
		() => {
			today.changed();
			chat.load().catch((err) => toasts.error(err));
		}
	);

	onMount(() => {
		Promise.all([today.refresh(), chat.load(), api.settings().then((v) => (view = v))]).catch((err) =>
			toasts.error(err)
		);
		stream.open();
		const clock = setInterval(() => (now = new Date()), 60_000);
		return () => {
			clearInterval(clock);
			stream.close();
		};
	});

	const t = $derived(today.today);
	const planned = $derived(t ? tiers(t, today.tasks) : null);
	const tray = $derived(t ? unplanned(t, today.tasks) : []);
	const done = $derived(t ? doneOn(t.date, today.tasks) : []);
	const endOfDay = $derived(
		t ? new Date(`${t.date}T${view?.settings.end_of_day ?? '17:30'}:00`) : new Date()
	);
	const pickTask = $derived(pick ? today.tasks.find((x) => x.id === pick?.task_id) : undefined);
	const tierNames: Tier[] = ['must', 'should', 'could'];

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

<div class="page">
	<main>
		{#if t}
			<Header
				today={t}
				events={today.events}
				{now}
				connected={stream.connected}
				configured={view ? configured(view) : true}
				{asking}
				onwhatnow={() => whatNow()}
			/>
			<DayStrip events={today.events} {now} {endOfDay} />
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

			{#if !t.plan}
				<StartDay />
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
					<DoneToday {done} meetings={survived(today.events, now)} />
				</section>
			</div>
		{:else}
			<p class="muted">Loading…</p>
		{/if}
	</main>
	<ChatDock opening={t?.plan?.opening ?? ''} />
</div>

<style>
	.page {
		display: flex;
		gap: 16px;
		padding: 16px;
		align-items: flex-start;
	}
	main {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
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
		.page {
			padding: 12px 12px 72px;
		}
		.lower {
			grid-template-columns: 1fr;
		}
	}
</style>
