<script lang="ts">
	import { api } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	const phrase = 'delete everything';
	let typed = $state('');
	let deleting = $state(false);

	async function wipe() {
		deleting = true;
		try {
			await api.deleteData();
			location.assign('/'); // the stream's data.reset does this too, unless it's down
		} catch (err) {
			toasts.error(err);
			deleting = false;
		}
	}
</script>

<section class="sticker danger">
	<h2 class="label">Danger zone</h2>
	<p>
		Deletes every task, event, day, plan, chat, and summary, your settings, the saved keys, and your
		integrations' saved addresses. There's no undo.
	</p>
	<label>
		<span class="muted">Type <strong>{phrase}</strong> to confirm</span>
		<input class="field" bind:value={typed} autocomplete="off" />
	</label>
	<button class="btn wipe" disabled={typed !== phrase || deleting} onclick={wipe}>
		{deleting ? 'Deleting…' : 'Delete everything'}
	</button>
</section>

<style>
	.danger {
		border-color: var(--danger);
		box-shadow: var(--shadow-danger);
		padding: 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	h2,
	p {
		margin: 0;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.wipe {
		align-self: flex-start;
		background: var(--danger);
		color: var(--on-primary);
	}
	.wipe:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
