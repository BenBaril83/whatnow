<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, type Day, type Review } from '#lib/api.ts';
	import Dots from '#lib/components/Dots.svelte';
	import TriageList from '#lib/components/TriageList.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { winLine } from '#lib/plan.ts';
	import { ritualOver } from '#lib/rituals.ts';
	import { addDays, survived } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { choicesFor, triagePayload, type Choices } from '#lib/triage.ts';
	import { today } from '#lib/today.svelte.ts';

	type Step = 'loading' | 'done' | 'unfinished' | 'note' | 'goodnight';

	let step = $state<Step>('loading');
	let review = $state<Review | null>(null);
	let choices = $state<Choices>({});
	let note = $state('');
	let notice = $state('');
	let wrapping = $state(false);
	let wrapped = $state<Day | null>(null);

	const unfinished = $derived((review?.unfinished.length ?? 0) > 0);
	const steps = $derived<Step[]>(unfinished ? ['done', 'unfinished', 'note', 'goodnight'] : ['done', 'note', 'goodnight']);
	const at = $derived(Math.max(0, steps.indexOf(step)));
	const tomorrow = $derived(today.today ? addDays(today.today.date, 1) : '');

	async function loadReview(kept: Choices = {}) {
		review = await api.wrapReview();
		choices = choicesFor(review, tomorrow, kept);
	}

	onMount(async () => {
		try {
			await today.refresh();
			const day = today.today?.day;
			// Nothing to wrap before the day starts, or once it's wrapped.
			if (!day || day.wrapped_at) return goto('/', { replaceState: true });
			await loadReview();
			step = 'done';
		} catch (err) {
			toasts.error(err);
		}
	});

	async function wrap() {
		wrapping = true;
		notice = '';
		try {
			wrapped = (await api.wrap(review ? triagePayload(choices) : [], note.trim())).data;
			await today.refresh();
			step = 'goodnight';
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				// Either another tab wrapped the day, or a task on the list changed elsewhere.
				await today.refresh().catch((e) => toasts.error(e));
				if (today.today && ritualOver('wrap', today.today)) return goto('/', { replaceState: true });
				await loadReview(choices).catch((e) => toasts.error(e));
				notice = "Something changed, here's the fresh list.";
				step = unfinished ? 'unfinished' : 'note';
			} else {
				toasts.error(err);
			}
		} finally {
			wrapping = false;
		}
	}
</script>

<Dots count={steps.length} {at} />

{#if step === 'loading'}
	<h1>Wrapping up</h1>
	<p class="muted center">Looking back at today…</p>
{:else if step === 'done' && review}
	<h1>{winLine(review.done.length, survived(today.events, live.now))}</h1>
	{#if review.done.length}
		<ul class="sticker done">
			{#each review.done as task (task.id)}<li>{task.title}</li>{/each}
		</ul>
	{:else}
		<p class="center">Some days are like that. Showing up counts.</p>
	{/if}
	<button class="btn primary next" onclick={() => (step = unfinished ? 'unfinished' : 'note')}>Next →</button>
{:else if step === 'unfinished' && review}
	<h1>What about the rest?</h1>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<TriageList {review} bind:choices />
	<div class="row">
		<button class="btn" onclick={() => (step = 'done')}>← Back</button>
		<button class="btn primary" onclick={() => (step = 'note')}>Next →</button>
	</div>
{:else if step === 'note'}
	<h1>Anything for tomorrow?</h1>
	{#if notice}<p class="chip notice">{notice}</p>{/if}
	<textarea class="field" rows="4" bind:value={note} placeholder="A note for tomorrow-you (optional)"></textarea>
	<div class="row">
		<button class="btn" onclick={() => (step = unfinished ? 'unfinished' : 'done')}>← Back</button>
		<button class="btn primary" disabled={wrapping} onclick={wrap}>{wrapping ? 'Wrapping up…' : 'Wrap up'}</button>
	</div>
{:else if step === 'goodnight'}
	<h1>Goodnight 🌙</h1>
	{#if wrapped?.reflection}<p class="sticker summary">{wrapped.reflection}</p>{/if}
	<button class="btn primary next" onclick={() => goto('/')}>Goodnight →</button>
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
	.done {
		margin: 0;
		padding: 14px 20px 14px 36px;
	}
	.done li {
		text-decoration: line-through;
		text-decoration-thickness: 2px;
	}
	.summary {
		margin: 0;
		padding: 14px 20px;
	}
	.row {
		display: flex;
		justify-content: space-between;
	}
	.next {
		align-self: flex-end;
	}
</style>
