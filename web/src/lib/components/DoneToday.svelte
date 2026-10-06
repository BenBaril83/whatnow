<script lang="ts">
	import type { Task } from '#lib/api.ts';
	import { winLine } from '#lib/plan.ts';

	interface Props {
		done: Task[];
		meetings: number;
	}
	let { done, meetings }: Props = $props();
</script>

<details class="sticker done">
	<summary>{winLine(done.length, meetings)}</summary>
	<ul>
		{#each done as task (task.id)}
			<li>{task.title}</li>
		{:else}
			<li class="muted">Nothing yet. That's allowed.</li>
		{/each}
	</ul>
</details>

<style>
	.done {
		padding: 10px 14px;
	}
	summary {
		font-weight: var(--heavy);
		cursor: pointer;
	}
	ul {
		margin: 8px 0 0;
		padding-left: 20px;
	}
	li {
		text-decoration: line-through;
		text-decoration-thickness: 2px;
	}
	li.muted {
		text-decoration: none;
	}
</style>
