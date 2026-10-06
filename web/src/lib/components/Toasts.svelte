<script lang="ts">
	import { toasts } from '#lib/toasts.svelte.ts';
</script>

<div class="stack" aria-live="polite">
	{#each toasts.items as toast (toast.id)}
		<div class="sticker toast" class:error={toast.error}>
			<span>{toast.text}</span>
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
	.toast span {
		flex: 1;
	}
	.error {
		background: var(--must);
	}
	.close {
		background: none;
		border: none;
		padding: 0 4px;
	}
</style>
