<script lang="ts">
	import { api, type PullResult, type PulledEvent } from '#lib/api.ts';
	import { withoutEvent } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		/** The integration the events came from. */
		id: string;
		result: PullResult;
		/** Pulls again, for a source that failed. */
		retry: () => void;
	}
	let { id, result = $bindable(), retry }: Props = $props();

	/** The recurring event whose × is asking whether to skip just today or the whole series. */
	let asking = $state<number | null>(null);

	const time = (d: string) => new Date(d).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });

	async function removeToday(e: PulledEvent) {
		asking = null;
		if (await toasts.track(`Removed: ${e.title}`, () => api.deleteEvent(e.id))) result = withoutEvent(result, e.id);
	}

	async function skip(e: PulledEvent) {
		asking = null;
		if (await toasts.track(`Skipping ${e.title} from now on`, () => api.skip(id, e.series, e.title, e.id))) {
			result = withoutEvent(result, e.id);
		}
	}
</script>

<div class="sources">
	{#each result.sources as source, i (i)}
		<section>
			<h3 class="label">{source.error ? source.label : `${source.label} · ${source.events.length}`}</h3>
			{#if source.error}
				<p class="bad">{source.error} <button class="link" onclick={retry}>Retry</button></p>
			{:else if source.events.length === 0}
				<p class="muted">Nothing today.</p>
			{:else}
				<ul>
					{#each source.events as e (e.id)}
						<li>
							<span class="at">{time(e.starts_at)}</span>
							<span class="title">{e.title}</span>
							{#if asking === e.id}
								<span class="ask">
									<button class="link" onclick={() => removeToday(e)}>Just today</button>
									<button class="link" onclick={() => skip(e)}>Always skip</button>
									<button class="link muted" onclick={() => (asking = null)}>Cancel</button>
								</span>
							{:else}
								<button
									class="remove"
									aria-label="Remove {e.title}"
									onclick={() => (e.series ? (asking = e.id) : removeToday(e))}>×</button
								>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/each}
</div>

<style>
	.sources {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
		gap: 12px;
	}
	h3 {
		margin: 0 0 4px;
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 8px;
		padding: 2px 0;
	}
	.at {
		font-weight: var(--heavy);
		white-space: nowrap;
	}
	.title {
		flex: 1;
		min-width: 0;
	}
	/* Its own line under the event, so the title stays readable. */
	.ask {
		flex-basis: 100%;
		display: flex;
		gap: 12px;
		flex-wrap: wrap;
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
	.link.muted {
		color: var(--ink-soft);
	}
	p {
		margin: 0;
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
