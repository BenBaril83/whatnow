<script lang="ts">
	/** Notification.permission, or denied where the browser has no notifications at all. */
	let permission = $state<NotificationPermission>(
		typeof Notification === 'undefined' ? 'denied' : Notification.permission
	);

	async function ask() {
		permission = await Notification.requestPermission();
	}
</script>

{#if permission === 'default'}
	<button type="button" class="btn" onclick={ask}>Turn on notifications</button>
{:else if permission === 'granted'}
	<span class="chip">Notifications on</span>
{:else}
	<span class="muted">Blocked in your browser. Nudges show in the page instead.</span>
{/if}
