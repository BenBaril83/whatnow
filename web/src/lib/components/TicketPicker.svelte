<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import { api, type PullResult, type Ticket } from '#lib/api.ts';
	import { picked, withoutTicket } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		/** The integration the tickets came from. */
		id: string;
		result: PullResult;
		/** Pulls again, for a pull that failed. */
		retry: () => void;
	}
	let { id, result = $bindable(), retry }: Props = $props();

	const ticked = new SvelteSet<string>();
	let added = $state(0);
	let adding = $state(false);

	function toggle(key: string) {
		if (ticked.has(key)) ticked.delete(key);
		else ticked.add(key);
	}

	async function add() {
		const tickets = picked(result, ticked);
		const n = tickets.length;
		adding = true;
		if (await toasts.track(`Added ${n} ${n === 1 ? 'task' : 'tasks'}`, () => api.addTickets(id, tickets))) added = n;
		adding = false;
	}

	async function skip(t: Ticket) {
		if (await toasts.track(`Skipping ${t.key} from now on`, () => api.skip(id, t.key, t.title))) {
			ticked.delete(t.key);
			result = withoutTicket(result, t.key);
		}
	}
</script>

<div class="tickets">
	{#if added}
		<p class="muted">Added {added}.</p>
	{:else}
		{#each result.sources as source, i (i)}
			<section>
				{#if source.error}
					<p class="bad">{source.error} <button class="link" onclick={retry}>Retry</button></p>
				{:else if source.tickets.length === 0}
					<p class="muted">Nothing new in {source.label}.</p>
				{:else}
					<h3 class="label">{`${source.label} · ${source.tickets.length}`}</h3>
					<ul>
						{#each source.tickets as t (t.key)}
							<li>
								<input
									type="checkbox"
									aria-label="Pick {t.key}"
									checked={ticked.has(t.key)}
									onchange={() => toggle(t.key)}
								/>
								<a class="title" href={t.url} target="_blank" rel="noopener noreferrer">{t.title}</a>
								<button class="remove" aria-label="Skip {t.key}" onclick={() => skip(t)}>×</button>
							</li>
						{/each}
					</ul>
					{#if source.more}<p class="muted">{source.more} more matched — narrow the search</p>{/if}
					<button class="btn primary" disabled={ticked.size === 0 || adding} onclick={add}>Add {ticked.size}</button>
				{/if}
			</section>
		{/each}
	{/if}
</div>

<style>
	.tickets {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	section {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 6px;
	}
	h3 {
		margin: 0;
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		width: 100%;
	}
	li {
		display: flex;
		align-items: baseline;
		gap: 8px;
		padding: 2px 0;
	}
	.title {
		flex: 1;
		min-width: 0;
		overflow-wrap: anywhere;
		color: inherit;
	}
	.remove {
		background: none;
		border: none;
		padding: 0 4px;
		font-weight: var(--heavy);
		color: var(--ink-soft);
	}
	.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
	}
	p {
		margin: 0;
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
