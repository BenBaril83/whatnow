<script lang="ts">
	import { api, type Pick, type Task } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		pick: Pick;
		task: Task | undefined;
		asking: boolean;
		/** Asks again without this task, with the user's optional reason. */
		onnotthis: (reason: string) => void;
		onclose: () => void;
	}
	let { pick, task, asking, onnotthis, onclose }: Props = $props();

	let explaining = $state(false);
	let reason = $state('');

	async function done() {
		if (await toasts.track(`Done: ${task?.title}`, () => api.setStatus(pick.task_id, 'done'))) {
			onclose();
		}
	}

	function notThis(e: SubmitEvent) {
		e.preventDefault();
		onnotthis(reason.trim());
		explaining = false;
		reason = '';
	}
</script>

<section class="sticker whatnow" aria-live="polite" data-task={pick.task_id}>
	<button class="close" aria-label="Close" onclick={onclose}>✕</button>
	<p class="label">Do this now</p>
	<h2>{task?.title ?? 'That task'}</h2>
	<p>{pick.reason}</p>
	{#if explaining}
		<form onsubmit={notThis}>
			<input class="field" bind:value={reason} placeholder="Why not? (optional)" aria-label="Why not this one" />
			<button class="btn" disabled={asking}>Ask again</button>
		</form>
	{:else}
		<div class="actions">
			<button class="btn primary" onclick={done}>Done</button>
			<button class="btn" disabled={asking} onclick={() => (explaining = true)}>
				{asking ? 'Thinking…' : 'Not this one'}
			</button>
		</div>
	{/if}
</section>

<style>
	.whatnow {
		position: relative;
		background: var(--chip);
		padding: 16px 20px;
	}
	h2 {
		margin: 4px 0;
		font-size: 22px;
		font-weight: var(--heavy);
	}
	.close {
		position: absolute;
		top: 10px;
		right: 12px;
		background: none;
		border: none;
	}
	.actions,
	form {
		display: flex;
		gap: 10px;
	}
</style>
