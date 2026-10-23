<script lang="ts">
	import ChatDock from '#lib/components/ChatDock.svelte';
	import InstallBanner from '#lib/components/InstallBanner.svelte';
	import EditSheet from '#lib/components/EditSheet.svelte';
	import { today } from '#lib/today.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();
</script>

<div class="page">
	<main>
		<InstallBanner />
		{@render children()}
	</main>
	<ChatDock opening={today.today?.plan?.opening ?? ''} />
</div>
<EditSheet />

<style>
	.page {
		display: flex;
		gap: 16px;
		padding: 16px;
		align-items: flex-start;
	}
	main {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}

	@media (max-width: 800px) {
		.page {
			padding: 12px 12px 72px;
		}
	}
</style>
