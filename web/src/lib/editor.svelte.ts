/** Which task the edit sheet has open, if any. Any card can open it. */
class Editor {
	taskId = $state<number | null>(null);

	open(id: number): void {
		this.taskId = id;
	}

	close(): void {
		this.taskId = null;
	}
}

export const editor = new Editor();
