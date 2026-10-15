<script lang="ts">
	import { api, type Tier } from '#lib/api.ts';
	import { dismiss } from '#lib/dismiss.ts';
	import { editor } from '#lib/editor.svelte.ts';
	import { linked } from '#lib/linked.svelte.ts';
	import { linkify } from '#lib/linkify.ts';
	import { links, type Card } from '#lib/plan.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';

	interface Props {
		card: Card;
		/** Absent for an unplanned card. */
		tier?: Tier;
		/** Today's date, for push to tomorrow. */
		date: string;
		/** All tasks shows dates, and opens the editor on a click instead of expanding the card. */
		listed?: boolean;
	}
	let { card, tier, date, listed = false }: Props = $props();

	let menuOpen = $state(false);
	let expanded = $state(false);
	const task = $derived(card.task);
	const chain = $derived(links(task.id, today.blockers, today.tasks));
	const movable = $derived(!listed && !!today.today?.plan && !today.today.day?.wrapped_at);
	const moves: { to: Tier | null; label: string }[] = [
		{ to: 'must', label: 'Must' },
		{ to: 'should', label: 'Should' },
		{ to: 'could', label: 'Could' },
		{ to: null, label: 'Unplanned' }
	];
	const showingLinks = $derived(linked.source === task.id);
	const titles = (tasks: { title: string }[]) => tasks.map((t) => t.title).join(', ');
	const toggleLinks = () => linked.toggle(task.id, [...chain.waitingOn, ...chain.unblocks].map((t) => t.id));

	// The card leaves once the refetch lands. If the write failed, untick it again.
	async function complete(e: Event & { currentTarget: HTMLInputElement }) {
		const box = e.currentTarget;
		if (!(await toasts.track(`Done: ${task.title}`, () => api.setStatus(task.id, 'done')))) {
			box.checked = false;
		}
	}
	function push() {
		menuOpen = false;
		toasts.track(`Pushed to tomorrow: ${task.title}`, () => api.push(task.id, addDays(date, 1)));
	}
	function drop() {
		menuOpen = false;
		toasts.track(`Dropped: ${task.title}`, () => api.setStatus(task.id, 'dropped'));
	}
	function move(to: Tier | null, label: string) {
		menuOpen = false;
		toasts.track(`Moved to ${label}: ${task.title}`, () => api.move(task.id, to));
	}
	function edit() {
		menuOpen = false;
		editor.open(task.id);
	}
	function restore() {
		toasts.track(`Restored: ${task.title}`, () => api.setStatus(task.id, 'open'));
	}
	const short = (date: string) =>
		new Date(`${date}T12:00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
</script>

<article
	data-task={task.id}
	class="sticker card {tier ?? (listed ? 'listed' : 'unplanned')}"
	class:closed={task.status !== 'open'}
	class:linked={linked.ids.includes(task.id)}
>
	{#if task.status === 'open'}
		<input type="checkbox" aria-label="Mark done" onchange={complete} />
	{:else}
		<span class="mark" aria-label={task.status}>{task.status === 'done' ? '✓' : '✕'}</span>
	{/if}
	<div class="main">
		<button
			class="body"
			onclick={() => (listed ? editor.open(task.id) : (expanded = !expanded))}
			disabled={!listed && !card.rationale && !task.notes}
			aria-expanded={listed ? undefined : expanded}
		>
			<span class="title">{task.title}</span>
			<span class="badges">
				{#if task.size}<span class="badge">{task.size}</span>{/if}
				{#if task.energy}<span class="badge">{task.energy} energy</span>{/if}
				{#if listed && task.due_on}<span class="badge">due {short(task.due_on)}</span>{/if}
				{#if listed && task.not_before}<span class="badge">from {short(task.not_before)}</span>{/if}
			</span>
			{#if task.feeling}<span class="feeling">"{task.feeling}"</span>{/if}
			{#if task.notes && !expanded}<span class="notes">{task.notes}</span>{/if}
			{#if card.rationale}<span class="why" class:shown={expanded}>{card.rationale}</span>{/if}
		</button>
		<!-- Expanded notes sit outside the button, so their links work and their text can be copied. -->
		{#if task.notes && expanded}
			<p class="notes shown">{#each linkify(task.notes) as s, i (i)}{#if s.href}<a href={s.href} target="_blank" rel="noopener noreferrer">{s.text}</a>{:else}{s.text}{/if}{/each}</p>
		{/if}
		{#if task.status === 'open' && (chain.waitingOn.length || chain.unblocks.length)}
			<div class="chain">
				{#if chain.waitingOn.length}
					<button
						class="badge link"
						aria-expanded={showingLinks}
						onclick={toggleLinks}
					>
						⛓ waits on {chain.waitingOn.length}
					</button>
				{/if}
				{#if chain.unblocks.length}
					<button
						class="badge link"
						aria-expanded={showingLinks}
						onclick={toggleLinks}
					>
						unblocks {chain.unblocks.length}
					</button>
				{/if}
			</div>
			{#if showingLinks}
				<p class="links">
					{#if chain.waitingOn.length}<span>Waiting on: {titles(chain.waitingOn)}</span>{/if}
					{#if chain.unblocks.length}<span>Unblocks: {titles(chain.unblocks)}</span>{/if}
				</p>
			{/if}
		{/if}
	</div>
	{#if task.status === 'dropped'}
		<button class="btn" onclick={restore}>Restore</button>
	{:else if task.status === 'open'}
		<details class="more" bind:open={menuOpen} {@attach dismiss}>
			<summary aria-label="More actions">⋯</summary>
			<div class="sticker menu">
				<button onclick={edit}>Edit</button>
				<button onclick={push}>Push to tomorrow</button>
				<button onclick={drop}>Drop</button>
				{#if movable}
					<details class="sub">
						<summary>Move ›</summary>
						{#each moves as m (m.label)}
							<button disabled={m.to === (tier ?? null)} onclick={() => move(m.to, m.label)}>{m.label}</button>
						{/each}
					</details>
				{/if}
			</div>
		</details>
	{/if}
</article>

<style>
	.card {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 10px 12px;
		margin-bottom: 10px;
	}
	.must {
		background: var(--must);
	}
	.should {
		background: var(--should);
	}
	.could {
		background: var(--could);
	}
	.unplanned {
		border-style: dashed;
		box-shadow: none;
	}
	.closed {
		opacity: 0.7;
	}
	.mark {
		width: 20px;
		text-align: center;
		font-weight: var(--heavy);
	}
	input[type='checkbox'] {
		width: 20px;
		height: 20px;
		margin-top: 2px;
		accent-color: var(--primary);
	}
	.main {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
	}
	.body {
		display: flex;
		flex-direction: column;
		gap: 3px;
		background: none;
		border: none;
		padding: 0;
		text-align: left;
		cursor: default;
	}
	.body:not(:disabled) {
		cursor: pointer;
	}
	.title {
		font-weight: var(--heavy);
	}
	.badges {
		display: flex;
		gap: 6px;
	}
	.badge {
		font-size: 11px;
		font-weight: var(--heavy);
		border: 1.5px solid var(--ink);
		border-radius: var(--pill);
		padding: 0 7px;
		background: var(--card);
	}
	.chain {
		display: flex;
		gap: 6px;
	}
	.link {
		cursor: pointer;
		color: var(--ink);
	}
	.link[aria-expanded='true'] {
		background: var(--chip);
	}
	.links {
		margin: 0;
		display: flex;
		flex-direction: column;
		font-size: 13px;
		color: var(--ink-soft);
	}
	.linked {
		outline: 2px dashed var(--primary);
		outline-offset: 3px;
	}
	.feeling {
		font-style: italic;
		font-size: 13px;
		color: var(--ink-soft);
	}
	.notes {
		font-size: 13px;
		white-space: pre-line;
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 1;
		line-clamp: 1;
		-webkit-box-orient: vertical;
	}
	.notes.shown {
		display: block;
		margin: 0;
		overflow-wrap: anywhere;
	}
	.why {
		display: none;
		font-size: 13px;
	}
	.why.shown,
	.card:hover .why {
		display: block;
	}
	.more {
		position: relative;
	}
	.more summary {
		list-style: none;
		cursor: pointer;
		font-weight: var(--heavy);
		padding: 0 4px;
	}
	.menu {
		position: absolute;
		right: 0;
		top: 24px;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 160px;
	}
	.menu button {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.menu button:hover:not(:disabled) {
		background: var(--chip);
	}
	.menu button:disabled {
		opacity: 0.4;
	}
	.sub summary {
		list-style: none;
		cursor: pointer;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.sub summary:hover {
		background: var(--chip);
	}
	.sub button {
		display: block;
		width: 100%;
		padding-left: 20px;
	}
</style>
