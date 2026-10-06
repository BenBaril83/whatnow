<script lang="ts">
	import { onMount } from 'svelte';
	import Capture from '#lib/components/Capture.svelte';
	import ChatDock from '#lib/components/ChatDock.svelte';
	import { chat } from '#lib/chat.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// The ritual is calm by default: chat waits as a tab. This doesn't touch the saved choice, so
	// Today opens the dock the way it was.
	onMount(() => {
		chat.open = false;
	});
</script>

<main class="ritual">
	{@render children()}
</main>
<div class="capture">
	<Capture />
</div>
<ChatDock opening="" />

<style>
	.ritual {
		max-width: 640px;
		margin: 0 auto;
		padding: 32px 16px 120px;
		display: flex;
		flex-direction: column;
		gap: 18px;
	}
	.capture {
		position: fixed;
		left: 50%;
		bottom: 16px;
		transform: translateX(-50%);
		width: min(640px, calc(100vw - 32px));
		z-index: 10;
	}

	@media (max-width: 800px) {
		.capture {
			bottom: 64px;
		}
	}
</style>
