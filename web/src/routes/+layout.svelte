<script lang="ts">
	import { onMount } from 'svelte';
	import '#lib/tokens.css';
	import favicon from '#lib/assets/favicon.svg';
	import Toasts from '#lib/components/Toasts.svelte';
	import { live } from '#lib/live.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	onMount(() => live.start());
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>whatnow</title>
</svelte:head>

{@render children()}
<Toasts />
