<script lang="ts">
	import { page } from '$app/state';
	import { signInURL } from '#lib/session.svelte.ts';

	const closed = $derived(page.url.searchParams.get('signups') === 'closed');
	const href = signInURL(Intl.DateTimeFormat().resolvedOptions().timeZone);
</script>

<main>
	<section class="sticker hero">
		<h1>whatnow</h1>
		<p>
			A day planner that answers one question: what should I do now? Dump your tasks and events, say how
			much energy you have, and get a plan for today, with a nudge for each next step.
		</p>
		<a class="btn primary" {href}>Sign in with Google</a>
		{#if closed}
			<p class="closed" role="status">Sign-ups are closed for now.</p>
		{/if}
	</section>
	<img class="sticker" src="/today.png" alt="Today's plan, sorted into must, should, and could" />
	<img class="sticker" src="/what-now.png" alt="What now? picks the next task" />
	<p class="foot"><a href="/privacy">Privacy</a></p>
</main>

<style>
	main {
		display: flex;
		flex-direction: column;
		gap: 16px;
		max-width: 760px;
		margin: 0 auto;
		padding: 16px;
	}
	.hero {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 12px;
		padding: 20px;
	}
	h1 {
		margin: 0;
		font-size: 32px;
		font-weight: var(--heavy);
	}
	.hero p {
		margin: 0;
		font-size: 17px;
	}
	a.btn {
		color: var(--on-primary);
		text-decoration: none;
	}
	.closed {
		font-weight: var(--heavy);
	}
	img {
		width: 100%;
		height: auto;
	}
	.foot {
		margin: 0;
		text-align: center;
	}
</style>
