<script lang="ts">
	import { page } from '$app/state';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	const sections = [
		{ href: '/settings', label: 'General' },
		{ href: '/settings/integrations', label: 'Integrations' }
	];
</script>

<main>
	<a href="/">← Today</a>
	<h1>Settings</h1>
	<div class="split">
		<nav aria-label="Settings sections">
			{#each sections as s (s.href)}
				<a href={s.href} class:on={page.url.pathname === s.href} aria-current={page.url.pathname === s.href ? 'page' : undefined}>
					{s.label}
				</a>
			{/each}
		</nav>
		<div class="content">
			{@render children()}
		</div>
	</div>
</main>

<style>
	main {
		max-width: 780px;
		margin: 0 auto;
		padding: 16px;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	h1 {
		margin: 0;
		font-weight: var(--heavy);
	}
	.split {
		display: grid;
		grid-template-columns: 150px minmax(0, 1fr);
		gap: 20px;
		align-items: start;
	}
	nav {
		display: flex;
		flex-direction: column;
		gap: 4px;
		position: sticky;
		top: 16px;
	}
	nav a {
		color: var(--ink);
		text-decoration: none;
		padding: 6px 10px;
		border: 2px solid transparent;
		border-radius: var(--radius-sm);
	}
	nav a.on {
		background: var(--card);
		border: var(--line);
		box-shadow: var(--shadow-sm);
	}
	.content {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	/* On a phone the sidebar becomes tabs across the top. */
	@media (max-width: 640px) {
		.split {
			grid-template-columns: minmax(0, 1fr);
		}
		nav {
			flex-direction: row;
			position: static;
		}
	}
</style>
