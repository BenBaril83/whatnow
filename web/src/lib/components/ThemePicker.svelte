<script lang="ts">
	import { api, type Theme } from '#lib/api.ts';
	import { live } from '#lib/live.svelte.ts';
	import { themes } from '#lib/theme.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		/** The Settings form's copy, kept in step so its Save doesn't put the old theme back. */
		theme: Theme;
	}
	let { theme = $bindable() }: Props = $props();

	// Saves at once, on top of the last saved settings rather than the form's unsaved edits. The
	// layout applies whatever live.view holds.
	async function pick(next: Theme) {
		if (!live.view) return;
		const prev = theme;
		theme = next;
		try {
			live.view = await api.saveSettings({ ...live.view.settings, theme: next });
		} catch (err) {
			toasts.error(err);
			theme = prev;
		}
	}
</script>

<section class="sticker">
	<h2 class="label">Look</h2>
	<div class="options">
		{#each themes as t (t.id)}
			<label class="option" class:on={theme === t.id}>
				<input
					type="radio"
					name="theme"
					value={t.id}
					checked={theme === t.id}
					disabled={!live.view}
					onchange={() => pick(t.id)}
				/>
				<span class="swatch" data-theme={t.id} aria-hidden="true">
					<span class="card"><span class="chip"></span><span class="go"></span></span>
				</span>
				{t.name}
			</label>
		{/each}
	</div>
</section>

<style>
	section {
		padding: 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	h2 {
		margin: 0;
		font-size: 14px;
	}
	.options {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: 10px;
	}
	.option {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
	}
	.on {
		font-weight: var(--heavy);
	}
	/* A tiny page in the theme's own tokens: its paper, a card, a chip, and the primary color. */
	.swatch {
		display: inline-flex;
		flex-shrink: 0;
		padding: 5px;
		background: var(--paper);
		border: 1px solid var(--ink-soft);
	}
	.card {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 3px 4px;
		background: var(--card);
		border: var(--line);
		border-radius: var(--radius-sm);
		box-shadow: var(--shadow-sm);
	}
	.chip,
	.go {
		width: 10px;
		height: 8px;
		border-radius: var(--pill);
	}
	.chip {
		background: var(--chip);
		border: var(--line);
	}
	.go {
		background: var(--primary);
	}
</style>
