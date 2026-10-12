<script lang="ts">
	import { api, type Integration, type PullResult } from '#lib/api.ts';
	import { formFrom, isOn, newRow } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import PullResults from './PullResults.svelte';
	import TicketPicker from './TicketPicker.svelte';

	interface Props {
		integration: Integration;
	}
	let { integration = $bindable() }: Props = $props();

	let open = $state(false);
	let form = $state(formFrom(integration));
	let saving = $state(false);
	let pulling = $state(false);
	let result = $state<PullResult | null>(null);

	/** Whether a list row's secret column is saved, so a blank input keeps it. */
	const savedSecret = (list: string, rowID: string, column: string) =>
		integration.lists[list]?.find((r) => r.id === rowID)?.secrets[column] ?? false;

	// The toggle saves at once, apart from the fields, so it never saves half-typed edits.
	async function toggle(e: Event & { currentTarget: HTMLInputElement }) {
		const input = e.currentTarget;
		try {
			integration = await api.enableIntegration(integration.id, input.checked);
			if (integration.enabled) open = true;
		} catch (err) {
			toasts.error(err);
			input.checked = integration.enabled;
		}
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			integration = await api.saveIntegration(integration.id, form);
			form = formFrom(integration);
		} catch (err) {
			toasts.error(err);
		} finally {
			saving = false;
		}
	}

	// The old result goes first, so a pick list starts over rather than keeping what was added.
	async function pull() {
		result = null;
		pulling = true;
		try {
			result = await api.pull(integration.id);
		} catch (err) {
			toasts.error(err);
		} finally {
			pulling = false;
		}
	}

	async function unskip(key: string) {
		try {
			integration = await api.unskip(integration.id, key);
		} catch (err) {
			toasts.error(err);
		}
	}
</script>

<section class="sticker card">
	<header>
		<button class="disclose" aria-expanded={open} onclick={() => (open = !open)}>
			{open ? '▾' : '▸'}
			{integration.name}
		</button>
		<label class="switch">
			<input type="checkbox" role="switch" checked={integration.enabled} onchange={toggle} />
			{integration.enabled ? 'On' : 'Off'}
		</label>
	</header>
	{#if open}
		<form onsubmit={save}>
			{#each integration.fields as f (f.key)}
				{#if f.kind === 'list'}
					<fieldset>
						<legend class="label">{f.label}</legend>
						{#each form.lists[f.key] as row, r (r)}
							<div class="row">
								{#each f.fields ?? [] as c (c.key)}
									{@const saved = c.kind === 'secret' && savedSecret(f.key, row.id, c.key)}
									<input
										class="field"
										class:secret={c.kind === 'secret'}
										type={c.kind === 'secret' ? 'password' : 'text'}
										autocomplete="off"
										aria-label={c.label}
										placeholder={saved ? '•••••••• saved' : c.label}
										bind:value={row.values[c.key]}
									/>
								{/each}
								<button type="button" class="link" onclick={() => form.lists[f.key].splice(r, 1)}>remove</button>
							</div>
						{/each}
						<button type="button" class="link" onclick={() => form.lists[f.key].push(newRow(f))}>+ Add</button>
						{#if f.help}<p class="muted hint">{f.help}</p>{/if}
					</fieldset>
				{:else}
					<label>
						<span class="label">
							{f.label}
							{#if f.kind === 'secret'}<span class="muted">({integration.secrets[f.key] ? 'saved' : 'not set'})</span>{/if}
						</span>
						<input
							class="field"
							type={f.kind === 'secret' ? 'password' : 'text'}
							autocomplete="off"
							placeholder={f.kind === 'secret' ? 'Leave blank to keep' : ''}
							bind:value={form.values[f.key]}
						/>
						{#if f.help}<span class="muted hint">{f.help}</span>{/if}
					</label>
				{/if}
			{/each}
			{#if integration.skipped.length}
				<div>
					<span class="label">Skipped</span>
					<ul class="skipped">
						{#each integration.skipped as s (s.key)}
							<li>{s.title} <button type="button" class="link" onclick={() => unskip(s.key)}>bring back</button></li>
						{/each}
					</ul>
				</div>
			{/if}
			<div class="actions">
				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
				<button type="button" class="btn" disabled={!isOn(integration) || pulling} onclick={pull}>
					{pulling ? 'Pulling…' : integration.pulls === 'tickets' ? 'Pull' : 'Pull today'}
				</button>
			</div>
		</form>
		{#if result && integration.pulls === 'tickets'}
			<TicketPicker id={integration.id} bind:result retry={pull} />
		{:else if result}
			<PullResults id={integration.id} bind:result retry={pull} />
		{/if}
	{/if}
</section>

<style>
	.card {
		padding: 14px 20px;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}
	.disclose {
		background: none;
		border: none;
		padding: 0;
		font-size: 17px;
		font-weight: var(--heavy);
	}
	.switch {
		display: flex;
		flex-direction: row;
		align-items: center;
		gap: 6px;
		font-weight: var(--heavy);
	}
	.switch input {
		appearance: none;
		width: 38px;
		height: 22px;
		border: var(--line);
		border-radius: var(--pill);
		background: var(--paper-deep);
		position: relative;
		cursor: pointer;
		margin: 0;
	}
	.switch input::after {
		content: '';
		position: absolute;
		top: 2px;
		left: 2px;
		width: 14px;
		height: 14px;
		border-radius: 50%;
		background: var(--ink);
		transition: left 0.15s;
	}
	.switch input:checked {
		background: var(--primary);
	}
	.switch input:checked::after {
		left: 18px;
		background: var(--on-primary);
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	fieldset {
		border: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 8px;
	}
	legend {
		padding: 0;
		margin-bottom: 6px;
	}
	.row {
		display: grid;
		grid-template-columns: minmax(80px, 1fr) 2fr auto;
		align-items: center;
		gap: 8px;
		width: 100%;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.hint {
		font-size: 13px;
		margin: 0;
	}
	.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
	}
	.skipped {
		margin: 4px 0 0;
		padding-left: 18px;
	}
	.actions {
		display: flex;
		gap: 10px;
		flex-wrap: wrap;
	}
</style>
