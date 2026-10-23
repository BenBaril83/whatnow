<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import '#lib/tokens.css';
	import '#lib/themes.css';
	// Vaporwave's face. A browser fetches these files only when a page uses the font.
	import '@fontsource/comic-neue/400.css';
	import '@fontsource/comic-neue/700.css';
	import '@fontsource/comic-neue/700-italic.css';
	import favicon from '#lib/assets/favicon.svg';
	import Landing from '#lib/components/Landing.svelte';
	import Toasts from '#lib/components/Toasts.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { session, showLanding } from '#lib/session.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { applyTheme } from '#lib/theme.ts';
	import { welcomeDue } from '#lib/welcome.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// A hosted server answers 401 to everything but the session until someone signs in, so the stream
	// starts only once the session says there's a user to stream for (or no sign-in at all).
	onMount(() => {
		let stop: (() => void) | undefined;
		session.load().then(
			() => {
				if (!session.value?.hosted || session.value.user) stop = live.start();
			},
			(err) => toasts.error(err)
		);
		return () => stop?.();
	});

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

{#if session.value}
	{#if showLanding(session.value, page.url.pathname)}
		<Landing />
	{:else}
		{@render children()}
	{/if}
{/if}
<Toasts />
