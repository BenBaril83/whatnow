<script lang="ts">
	interface Props {
		/** What's happening, said plainly. */
		label: string;
	}
	let { label }: Props = $props();
</script>

<p class="working" role="status">
	<span class="dots" aria-hidden="true"><span></span><span></span><span></span></span>
	{label}
</p>

<style>
	.working {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 10px;
		margin: 8px 0;
		color: var(--ink-soft);
		font-weight: var(--heavy);
	}
	.dots {
		display: inline-flex;
		gap: 5px;
	}
	.dots span {
		width: 9px;
		height: 9px;
		border: var(--line);
		border-radius: var(--pill);
		background: var(--primary);
		animation: hop 1s ease-in-out infinite;
	}
	.dots span:nth-child(2) {
		background: var(--now);
		animation-delay: 0.15s;
	}
	.dots span:nth-child(3) {
		background: var(--chip);
		animation-delay: 0.3s;
	}
	@keyframes hop {
		0%,
		60%,
		100% {
			transform: translateY(0);
		}
		30% {
			transform: translateY(-6px);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.dots span {
			animation: none;
		}
	}
</style>
