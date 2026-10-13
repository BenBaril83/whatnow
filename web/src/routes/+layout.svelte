<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import '#lib/tokens.css';
	import '#lib/themes.css';
	import favicon from '#lib/assets/favicon.svg';
	import Toasts from '#lib/components/Toasts.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { applyTheme } from '#lib/theme.ts';
	import { welcomeDue } from '#lib/welcome.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	onMount(() => live.start());

	$effect(() => {
		if (live.view) applyTheme(live.view.settings.theme);
	});

	$effect(() => {
		if (live.view && welcomeDue(live.view.settings, page.url.pathname)) goto('/welcome', { replaceState: true });
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>whatnow</title>
</svelte:head>

{@render children()}
<Toasts />
