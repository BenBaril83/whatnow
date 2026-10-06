<script lang="ts">
	interface Props {
		style: string;
		aboutMe: string;
	}
	let { style = $bindable(), aboutMe = $bindable() }: Props = $props();
</script>

<label>
	<span class="label">Assistant style <span class="muted">({style.length}/500)</span></span>
	<span class="muted hint">How it talks to you, like "gentle and brief" or "a drill sergeant who cares."</span>
	<textarea class="field" rows="2" maxlength="500" bind:value={style}></textarea>
</label>
<label>
	<span class="label">About me <span class="muted">({aboutMe.length}/1000)</span></span>
	<span class="muted hint">What drains you and what's easy. It shapes how plans weigh each task.</span>
	<textarea class="field" rows="4" maxlength="1000" bind:value={aboutMe}></textarea>
</label>

<style>
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.hint {
		font-size: 13px;
	}
</style>
