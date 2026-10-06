<script lang="ts">
	interface Props {
		count: number;
		/** Zero-based. */
		at: number;
	}
	let { count, at }: Props = $props();
</script>

<div class="dots" aria-label="Step {at + 1} of {count}">
	{#each { length: count }, i (i)}
		<span class:on={i <= at}></span>
	{/each}
</div>

<style>
	.dots {
		display: flex;
		justify-content: center;
		gap: 8px;
	}
	span {
		width: 12px;
		height: 12px;
		border-radius: 50%;
		border: var(--line);
	}
	.on {
		background: var(--ink);
	}
</style>
