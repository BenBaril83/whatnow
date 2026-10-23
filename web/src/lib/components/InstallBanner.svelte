<script lang="ts">
	import { onMount } from 'svelte';
	import { installBannerDue, installDismissedKey, installPlatform } from '#lib/install.ts';
	import { session } from '#lib/session.svelte.ts';

	let standalone = $state(false);
	let dismissed = $state(false);
	let platform = $state<'ios' | 'android' | 'other'>('other');

	onMount(() => {
		standalone = matchMedia('(display-mode: standalone)').matches;
		platform = installPlatform(navigator.userAgent);
		try {
			dismissed = localStorage.getItem(installDismissedKey) !== null;
		} catch {}
	});

	function dismiss() {
		dismissed = true;
		try {
			localStorage.setItem(installDismissedKey, '1');
		} catch {}
	}
</script>

{#if installBannerDue(session.value?.hosted ?? false, standalone, dismissed)}
	<aside class="card banner" aria-label="Add whatnow to your home screen">
		<div>
			<strong>Keep whatnow on your home screen</strong>
			{#if platform !== 'android'}
				<p><b>iPhone or iPad:</b> tap Share, then Add to Home Screen.</p>
			{/if}
			{#if platform !== 'ios'}
				<p><b>Android:</b> open the browser menu, then tap Install app.</p>
			{/if}
		</div>
		<button type="button" class="btn" aria-label="Dismiss" onclick={dismiss}>&times;</button>
	</aside>
{/if}

<style>
	.banner {
		display: flex;
		gap: 12px;
		align-items: flex-start;
		justify-content: space-between;
		padding: 12px 14px;
		background: var(--chip);
	}
	p {
		margin: 4px 0 0;
	}
</style>
