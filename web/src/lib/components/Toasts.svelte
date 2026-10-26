<script lang="ts">
	import { onMount } from 'svelte';
	import { toasts } from '#lib/toasts.svelte.ts';

	onMount(() => toasts.hidden(document.visibilityState === 'hidden'));
</script>

<svelte:document onvisibilitychange={() => toasts.hidden(document.visibilityState === 'hidden')} />

<div class="stack" aria-live="polite">
	{#each toasts.items as toast (toast.id)}
		<div
			class="sticker toast"
			class:error={toast.error}
			class:nudge={toast.nudge}
			class:paused={toast.paused}
			style:--lifetime="{toast.life}ms"
			role="status"
			onpointerenter={() => toasts.pause(toast.id)}
			onpointerleave={() => toasts.resume(toast.id)}
			onfocusin={() => toasts.pause(toast.id)}
			onfocusout={() => toasts.resume(toast.id)}
		>
			<span class="fill" aria-hidden="true"></span>
			<span class="text">{toast.text}</span>
			{#if toast.action}
				<button
					class="btn primary"
					onclick={() => {
						toast.action?.run();
						toasts.dismiss(toast.id);
					}}>{toast.action.label}</button
				>
			{/if}
			{#if toast.batch !== undefined}
				<button class="btn" onclick={() => toasts.undo(toast)}>Undo</button>
			{/if}
			<button class="close" aria-label="Dismiss" onclick={() => toasts.dismiss(toast.id)}>✕</button>
		</div>
	{/each}
</div>

<style>
	.stack {
		position: fixed;
		left: 16px;
		bottom: 16px;
		display: flex;
		flex-direction: column;
		gap: 8px;
		z-index: 30;
		max-width: min(420px, calc(100vw - 32px));
	}
	.toast {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		font-weight: var(--heavy);
	}
	.toast {
		position: relative;
		overflow: hidden;
	}
	.text {
		flex: 1;
	}
	.toast > :not(.fill) {
		position: relative;
	}
	/* Sweeps left to right over the toast's lifetime, so you can see how long it has left. */
	.fill {
		position: absolute;
		inset: 0;
		background: var(--primary);
		opacity: 0.14;
		transform-origin: left;
		animation: fill var(--lifetime) linear forwards;
	}
	.paused .fill {
		animation-play-state: paused;
	}
	@keyframes fill {
		from {
			transform: scaleX(0);
		}
		to {
			transform: scaleX(1);
		}
	}
	.error {
		background: var(--must);
	}
	.nudge {
		background: var(--chip);
	}
	.close {
		background: none;
		border: none;
		padding: 0 4px;
	}
</style>
