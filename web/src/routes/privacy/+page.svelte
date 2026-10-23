<svelte:head>
	<title>Privacy · whatnow</title>
</svelte:head>

<main class="sticker">
	<h1>Privacy</h1>

	<h2>What's stored</h2>
	<p>
		Your tasks, your calendar events, how you feel about them, and your settings. Each person's data is
		kept in a file of their own.
	</p>

	<h2>Your API keys</h2>
	<p>The model provider keys you paste in are encrypted before they are saved.</p>

	<h2>Your model provider</h2>
	<p>
		To plan your day and answer in chat, whatnow sends what's needed, such as your tasks and messages, to
		the model provider you chose. That provider sees it and handles it under its own terms.
	</p>

	<h2>Backups</h2>
	<p>Everything is backed up, and backups are kept for 30 days.</p>

	<h2>Deleting your account</h2>
	<p>
		In Settings, use Delete my account. It removes your data and your sign-in. Copies already in backups
		expire within 30 days.
	</p>

	<p><a href="/">Back to whatnow</a></p>
</main>

<style>
	main {
		max-width: 680px;
		margin: 16px auto;
		padding: 8px 20px;
	}
	h1 {
		font-weight: var(--heavy);
	}
	h2 {
		margin-bottom: 4px;
		font-size: 17px;
	}
	p {
		margin-top: 0;
	}
</style>
