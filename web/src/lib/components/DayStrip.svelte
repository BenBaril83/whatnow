<script lang="ts">
	import type { CalendarEvent } from '#lib/api.ts';
	import { strip } from '#lib/time.ts';

	interface Props {
		events: CalendarEvent[];
		now: Date;
		endOfDay: Date;
		/** When the day was started, which anchors the strip's left edge. */
		startedAt: string | null;
	}
	let { events, now, endOfDay, startedAt }: Props = $props();

	const layout = $derived(strip(events, now, endOfDay, startedAt));
	const time = (d: Date | string) =>
		new Date(d).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
</script>

<div class="sticker strip" aria-label="Today's events">
	{#each layout.ticks as tick (tick.label)}
		<div class="tick" style:left="{tick.left}%"><span>{tick.label}</span></div>
	{/each}
	<div class="eod" style:left="{layout.endOfDay}%"><span>{time(endOfDay)}</span></div>
	{#each layout.items as item (item.event.id)}
		<div
			class="event"
			style:left="{item.left}%"
			style:width="{item.width}%"
			title="{time(item.event.starts_at)} {item.event.title}"
		>
			<span class="at">{time(item.event.starts_at)}</span>
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
		height: 62px;
		overflow: hidden;
	}
	.tick,
	.eod {
		position: absolute;
		top: 0;
		bottom: 0;
		border-left: 1px solid var(--paper-deep);
	}
	.eod {
		border-left: 2px dashed var(--ink-soft);
	}
	.tick span,
	.eod span {
		position: absolute;
		bottom: 2px;
		left: 3px;
		font-size: 10px;
		font-weight: var(--heavy);
		color: var(--ink-soft);
		white-space: nowrap;
	}
	.event {
		position: absolute;
		top: 6px;
		bottom: 20px;
		background: var(--event);
		border: var(--line);
		border-radius: var(--radius-sm);
		padding: 4px 6px;
		/* A short event grows to fit its title rather than cutting it off. */
		min-width: max-content;
		font-size: 12px;
		font-weight: var(--heavy);
		white-space: nowrap;
	}
	.at {
		font-weight: normal;
		margin-right: 4px;
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
