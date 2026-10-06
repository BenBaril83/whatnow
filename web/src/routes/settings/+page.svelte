<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type ConnectionResult, type Provider, type SettingsView } from '#lib/api.ts';
	import { live } from '#lib/live.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	let view = $state<SettingsView | null>(null);
	let key = $state('');
	let saving = $state(false);
	let testing = $state(false);
	let result = $state<ConnectionResult | null>(null);

	// Before any provider is saved, the form starts on the default one.
	onMount(() => {
		api.settings().then(
			(v) => {
				v.settings.provider ||= 'anthropic';
				view = v;
			},
			(err) => toasts.error(err)
		);
	});

	const provider = $derived((view?.settings.provider || 'anthropic') as Provider);
	const keyNote = $derived(
		view?.keys[provider] === 'env'
			? 'from env'
			: view?.keys[provider] === 'keychain'
				? 'saved'
				: 'not set'
	);

	// PUT takes the whole settings object, so this sends back everything GET returned.
	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!view) return;
		saving = true;
		result = null;
		try {
			if (key.trim()) {
				await api.saveKey(provider, key.trim());
				key = '';
			}
			view = await api.saveSettings(view.settings);
			live.view = view;
		} catch (err) {
			toasts.error(err);
		} finally {
			saving = false;
		}
	}

	async function test() {
		testing = true;
		try {
			result = await api.testConnection();
		} catch (err) {
			toasts.error(err);
		} finally {
			testing = false;
		}
	}
</script>

<main>
	<a href="/">← Today</a>
	<h1>Settings</h1>
	{#if view}
		<form class="sticker" onsubmit={save}>
			<label>
				<span class="label">Provider</span>
				<select class="field" bind:value={view.settings.provider}>
					<option value="anthropic">Anthropic</option>
					<option value="openai">OpenAI-compatible</option>
				</select>
			</label>
			<label>
				<span class="label">API key <span class="muted">({keyNote})</span></span>
				<input class="field" type="password" bind:value={key} autocomplete="off" placeholder="Leave blank to keep" />
			</label>
			<label>
				<span class="label">Main model</span>
				<input class="field" bind:value={view.settings[provider].main_model} />
			</label>
			<label>
				<span class="label">Light model</span>
				<input class="field" bind:value={view.settings[provider].light_model} />
			</label>
			{#if provider === 'openai'}
				<label>
					<span class="label">Base URL</span>
					<input class="field" bind:value={view.settings.openai.base_url} placeholder="https://api.openai.com/v1" />
				</label>
			{/if}
			<div class="actions">
				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
				<button type="button" class="btn" disabled={testing} onclick={test}>
					{testing ? 'Testing…' : 'Test connection'}
				</button>
				{#if result}
					<span class:ok={result.ok} class:bad={!result.ok}>
						{result.ok ? 'Connected' : result.error}
					</span>
				{/if}
			</div>
		</form>
	{:else}
		<p class="muted">Loading…</p>
	{/if}
</main>

<style>
	main {
		max-width: 560px;
		margin: 0 auto;
		padding: 16px;
	}
	h1 {
		font-weight: var(--heavy);
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
		padding: 20px;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
	}
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
