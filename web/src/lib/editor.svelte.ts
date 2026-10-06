/** Which task the edit sheet has open, if any, or whether it's making a new one. */
class Editor {
	taskId = $state<number | null>(null);
	creating = $state(false);

	open(id: number): void {
		this.taskId = id;
		this.creating = false;
	}

	/** Opens the sheet on an empty form, for a task made without the model. */
	openNew(): void {
		this.taskId = null;
		this.creating = true;
	}

	close(): void {
		this.taskId = null;
		this.creating = false;
	}
}

export const editor = new Editor();
