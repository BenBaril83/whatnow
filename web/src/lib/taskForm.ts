import type { Energy, Size, Task, TaskFields } from './api';

/** The edit form's values. Every field is a string; empty means unset. */
export interface TaskForm {
	title: string;
	notes: string;
	size: Size | '';
	energy: Energy | '';
	feeling: string;
	due_on: string;
	not_before: string;
}

export function blankForm(): TaskForm {
	return { title: '', notes: '', size: '', energy: '', feeling: '', due_on: '', not_before: '' };
}

export function formFor(task: Task): TaskForm {
	return {
		title: task.title,
		notes: task.notes,
		size: task.size ?? '',
		energy: task.energy ?? '',
		feeling: task.feeling,
		due_on: task.due_on ?? '',
		not_before: task.not_before ?? ''
	};
}

/**
 * The write for a form, editing `task` or making a new one when it's null. The server replaces
 * every field, so an edited task's refs go back unchanged. A blank title is refused here, since
 * the server's message for it would mention raw text.
 */
export function fieldsFrom(form: TaskForm, task: Task | null): { fields: TaskFields } | { error: string } {
	const title = form.title.trim();
	if (!title) return { error: 'A title is required.' };
	return {
		fields: {
			title,
			notes: form.notes,
			size: form.size || null,
			energy: form.energy || null,
			feeling: form.feeling,
			due_on: form.due_on || null,
			not_before: form.not_before || null,
			refs: task?.refs ?? []
		}
	};
}
