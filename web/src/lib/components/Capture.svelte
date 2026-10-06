<script lang="ts">
	import { api } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	let text = $state('');

	// The box clears at once: the raw task is saved before cleanup ever runs, so capture never
	// waits on a model. A failed save puts the text back rather than losing it.
	async function capture(e: SubmitEvent) {
		e.preventDefault();
		const raw = text.trim();
		if (!raw) return;
		text = '';
		if (!(await toasts.track('Captured', () => api.capture(raw))) && !text) text = raw;
	}
</script>

<form class="capture" onsubmit={capture}>
	<input class="field" bind:value={text} placeholder="Capture anything… press Enter" aria-label="Capture" />
</form>

<style>
	.capture input {
		box-shadow: var(--shadow-sm);
		padding: 12px 14px;
	}
</style>
