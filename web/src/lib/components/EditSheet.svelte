<script lang="ts">
	import { editor } from '#lib/editor.svelte.ts';
	import { today } from '#lib/today.svelte.ts';
	import TaskEditor from './TaskEditor.svelte';

	const task = $derived(today.tasks.find((t) => t.id === editor.taskId));
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && editor.close()} />

{#if task || editor.creating}
	<div class="scrim" role="presentation" onclick={() => editor.close()}></div>
	<div class="sticker sheet" role="dialog" aria-modal="true" aria-label={task ? 'Edit task' : 'New task'}>
		{#key task?.id ?? 'new'}
			<TaskEditor task={task ?? null} />
		{/key}
	</div>
{/if}

<style>
	.scrim {
		position: fixed;
		inset: 0;
		background: color-mix(in srgb, var(--ink) 40%, transparent);
		z-index: 40;
	}
	.sheet {
		position: fixed;
		top: 5vh;
		left: 50%;
		transform: translateX(-50%);
		width: min(560px, calc(100vw - 32px));
		max-height: 90vh;
		overflow-y: auto;
		z-index: 41;
		background: var(--paper);
		padding: 18px 20px;
	}
</style>
