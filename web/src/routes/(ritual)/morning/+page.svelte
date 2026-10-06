<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, paceLabels, type Review, type Tier } from '#lib/api.ts';
	import Dots from '#lib/components/Dots.svelte';
	import TriageList from '#lib/components/TriageList.svelte';
	import { tiers } from '#lib/plan.ts';
	import { ritualOver } from '#lib/rituals.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { choicesFor, triagePayload, type Choices } from '#lib/triage.ts';
	import { today } from '#lib/today.svelte.ts';

	type Step = 'greet' | 'reconcile' | 'pace' | 'making' | 'reveal';

	let step = $state<Step>('greet');
	let review = $state<Review | null>(null);
	let choices = $state<Choices>({});
	let pace = $state(3);
	let mood = $state('');
	let notice = $state('');

	const reconciling = $derived((review?.unfinished.length ?? 0) > 0);
	const steps = $derived<Step[]>(reconciling ? ['reconcile', 'pace', 'reveal'] : ['pace', 'reveal']);
	const at = $derived(Math.max(0, steps.indexOf(step === 'making' ? 'reveal' : step)));
	const tomorrow = $derived(today.today ? addDays(today.today.date, 1) : '');
	const revealed = $derived(today.today ? tiers(today.today, today.tasks) : null);
	const tierNames: Tier[] = ['must', 'should', 'could'];

	async function loadReview(kept: Choices = {}) {
		review = (await api.morning()).review;
		if (review) choices = choicesFor(review, tomorrow, kept);
	}

	onMount(async () => {
		try {
			await today.refresh();
			// A day that already started has nothing to check in for.
			if (today.today?.plan) return goto('/', { replaceState: true });
			await loadReview();
			step = reconciling ? 'reconcile' : 'pace';
		} catch (err) {
			toasts.error(err);
			step = 'pace';
		}
	});

	async function start() {
		step = 'making';
		notice = '';
		try {
			await api.start(pace, mood.trim(), review ? triagePayload(choices) : []);
			await today.refresh();
			step = 'reveal';
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				// Either another tab started the day, or a task on the list changed elsewhere.
				await today.refresh().catch((e) => toasts.error(e));
				if (today.today && ritualOver('morning', today.today)) return goto('/', { replaceState: true });
				await loadReview(choices).catch((e) => toasts.error(e));
				notice = "Something changed, here's the fresh list.";
				step = reconciling ? 'reconcile' : 'pace';
			} else {
				toasts.error(err);
				step = 'pace';
			}
		}
	}
</script>

<Dots count={steps.length} {at} />

{#if step === 'greet'}
	<h1>Good morning ☀︎</h1>
	<p class="muted center">Looking at where things stand…</p>
{:else if step === 'reconcile' && review}
	<h1>Good morning ☀︎</h1>
	<p class="center">
		{#if review.done.length}Last time you got {review.done.length} {review.done.length === 1 ? 'thing' : 'things'} done.{/if}
		Here's what's still open. What should happen to each?
	</p>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<TriageList {review} bind:choices />
	<button class="btn primary next" onclick={() => (step = 'pace')}>Next →</button>
{:else if step === 'pace'}
	<h1>How's the tank?</h1>
	{#if notice && !reconciling}<p class="chip notice">{notice}</p>{/if}
	<div class="paces">
		{#each paceLabels as label, i (label)}
			<button class="chip" class:on={pace === i + 1} onclick={() => (pace = i + 1)}>{label}</button>
		{/each}
	</div>
	<textarea class="field" rows="3" bind:value={mood} placeholder="Anything on your mind? (optional)"></textarea>
	<div class="row">
		{#if reconciling}<button class="btn" onclick={() => (step = 'reconcile')}>← Back</button>{/if}
		<button class="btn primary" onclick={start}>Plan my day →</button>
	</div>
{:else if step === 'making'}
	<h1>Making your plan…</h1>
	<p class="muted center">This takes a few seconds.</p>
{:else if step === 'reveal' && today.today?.plan && revealed}
	<p class="opening">{today.today.plan.opening || "Here's today."}</p>
	<div class="sticker reveal">
		{#each tierNames as tier (tier)}
			{#if revealed[tier].length}
				<p class="label">{tier}</p>
				<ul>
					{#each revealed[tier] as card (card.task.id)}<li>{card.task.title}</li>{/each}
				</ul>
			{/if}
		{/each}
	</div>
	<button class="btn primary next" onclick={() => goto('/')}>Let's go →</button>
{/if}

<style>
	h1 {
		margin: 0;
		text-align: center;
		font-size: 30px;
		font-weight: var(--heavy);
	}
	.center {
		text-align: center;
		margin: 0;
	}
	.notice {
		align-self: center;
		margin: 0;
	}
	.paces {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: 8px;
	}
	.chip {
		background: var(--card);
		cursor: pointer;
		font-size: 15px;
		padding: 4px 14px;
	}
	.chip.on {
		background: var(--chip);
	}
	.row {
		display: flex;
		justify-content: space-between;
	}
	.row .primary:only-child {
		margin-left: auto;
	}
	.next {
		align-self: flex-end;
	}
	.opening {
		font-size: 22px;
		font-weight: var(--heavy);
		text-align: center;
		margin: 0;
	}
	.reveal {
		padding: 14px 20px;
	}
	.reveal ul {
		margin: 4px 0 12px;
	}
</style>
