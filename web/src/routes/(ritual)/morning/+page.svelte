<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, paceLabels, type Review, type Tier } from '#lib/api.ts';
	import Dots from '#lib/components/Dots.svelte';
	import Working from '#lib/components/Working.svelte';
	import Tagline from '#lib/components/Tagline.svelte';
	import TriageList from '#lib/components/TriageList.svelte';
	import { tiers } from '#lib/plan.ts';
	import { ritualOver } from '#lib/rituals.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { choicesFor, triagePayload, type Choices } from '#lib/triage.ts';
	import { today } from '#lib/today.svelte.ts';

	type Step = 'greet' | 'reconcile' | 'dump' | 'pace' | 'making' | 'reveal';

	let step = $state<Step>('greet');
	let review = $state<Review | null>(null);
	let choices = $state<Choices>({});
	let pace = $state(3);
	let mood = $state('');
	let dump = $state('');
	let notice = $state('');
	/** The plan is taking longer than usual, as when the model is slow or retrying. */
	let slow = $state(false);

	const reconciling = $derived((review?.unfinished.length ?? 0) > 0);
	const steps = $derived<Step[]>(reconciling ? ['reconcile', 'dump', 'pace', 'reveal'] : ['dump', 'pace', 'reveal']);
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
			step = reconciling ? 'reconcile' : 'dump';
		} catch (err) {
			toasts.error(err);
			step = 'dump';
		}
	});

	async function start() {
		step = 'making';
		notice = '';
		const timer = setTimeout(() => (slow = true), 10_000);
		try {
			await api.start(pace, mood.trim(), review ? triagePayload(choices) : [], dump.trim());
			await today.refresh();
			step = 'reveal';
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				// The server captures the dump before it can refuse the start, so it's saved already.
				dump = '';
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
		} finally {
			clearTimeout(timer);
			slow = false;
		}
	}
</script>

<Dots count={steps.length} {at} />
{#if step === 'greet' || step === steps[0]}<Tagline />{/if}

{#if step === 'greet'}
	<h1>Good morning ☀︎</h1>
	<Working label="Looking at where things stand…" />
{:else if step === 'reconcile' && review}
	<h1>Good morning ☀︎</h1>
	<p class="center">
		{#if review.done.length}Last time you got {review.done.length} {review.done.length === 1 ? 'thing' : 'things'} done.{/if}
		Here's what's still open. What should happen to each?
	</p>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<TriageList {review} bind:choices />
	<button class="btn primary next" onclick={() => (step = 'dump')}>Next →</button>
{:else if step === 'dump'}
	<h1>What's on your plate?</h1>
	<p class="muted center">Dump it all here, messy is fine. I'll sort it into tasks before planning.</p>
	<textarea class="field" rows="8" bind:value={dump} aria-label="What's on your plate"></textarea>
	<div class="row">
		{#if reconciling}<button class="btn" onclick={() => (step = 'reconcile')}>← Back</button>{/if}
		<button class="link" onclick={() => ((dump = ''), (step = 'pace'))}>Skip</button>
		<button class="btn primary" onclick={() => (step = 'pace')}>Next →</button>
	</div>
{:else if step === 'pace'}
	<h1>How's the tank?</h1>
	{#if notice && !reconciling}<p class="chip notice">{notice}</p>{/if}
	<div class="paces">
		{#each paceLabels as label, i (label)}
			<button class="chip" class:on={pace === i + 1} onclick={() => (pace = i + 1)}>{label}</button>
		{/each}
	</div>
	<textarea class="field" rows="3" bind:value={mood} placeholder="How are you feeling? (optional)"></textarea>
	<div class="row">
		<button class="btn" onclick={() => (step = 'dump')}>← Back</button>
		<button class="btn primary" onclick={start}>Plan my day →</button>
	</div>
{:else if step === 'making'}
	<h1>Making your plan</h1>
	<Working label={slow ? 'Still thinking. Big days take a little longer.' : 'This takes a few seconds…'} />
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
	.link {
		margin-left: auto;
		margin-right: 12px;
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
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
