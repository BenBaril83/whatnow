<script lang="ts">
	import type { CalendarEvent } from '#lib/api.ts';
	import { strip } from '#lib/time.ts';

	interface Props {
		events: CalendarEvent[];
		now: Date;
		endOfDay: Date;
	}
	let { events, now, endOfDay }: Props = $props();

	const layout = $derived(strip(events, now, endOfDay));
	const time = (iso: string) =>
		new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
</script>

<div class="sticker strip" aria-label="Today's events">
	{#each layout.items as item (item.event.id)}
		<div
			class="event"
			style:left="{item.left}%"
			style:width="{item.width}%"
			title="{time(item.event.starts_at)} {item.event.title}"
		>
			{item.event.title}
		</div>
	{/each}
	<div class="now" style:left="{layout.now}%"></div>
	{#if events.length === 0}
		<span class="empty muted">No events today. Wide open.</span>
	{/if}
</div>

<style>
	.strip {
		position: relative;
		height: 48px;
		overflow: hidden;
	}
	.event {
		position: absolute;
		top: 8px;
		bottom: 8px;
		background: var(--event);
		border: var(--line);
		border-radius: var(--radius-sm);
		padding: 4px 6px;
		/* A short event grows to fit its title rather than cutting it off. Hover gives the time. */
		min-width: max-content;
		font-size: 12px;
		font-weight: var(--heavy);
		white-space: nowrap;
	}
	.event:hover {
		z-index: 2;
	}
	.now {
		position: absolute;
		top: 0;
		bottom: 0;
		width: 3px;
		background: var(--now);
		z-index: 1;
	}
	.empty {
		position: absolute;
		left: 16px;
		top: 13px;
		font-size: 14px;
	}
</style>
