# Contributing to whatnow

Thanks for helping. Issues and PRs are both welcome. For anything bigger than a fix, open an issue
first so we can agree on the shape before you build it.

## License

whatnow is [AGPL-3.0](LICENSE). By opening a PR you agree your contribution is licensed under it
too, and that you have the right to contribute it. You keep the copyright on what you write.

## Before you open a PR

Run these from the repo root, as in the README's [Development](README.md#development) section:

```sh
just test
just lint
just e2e
```

Say in the PR what you tested by hand, and what you didn't.

## Style

- Read the code around your change and write like it: its naming, its comment density, its idioms.
  New code should look like it was always there.
- Keep the diff to what the PR is for. Don't reformat or rename neighboring code.
- Comments say why, not what. Errors shown to the user are plain sentences, and never contain a
  token or key.
- Go: `go vet` clean, table tests where they fit, tests named for the behavior (`TestPullOffersAssignedIssuesMineFirst`).
- Web: TypeScript, Svelte 5 runes, Vitest names that start with "Should".
- Test behavior someone could break. Don't write a test that only restates the code.
- A new feature gets a short design spec in `docs/superpowers/specs/`, like the ones there.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
  `fix:`, `docs:`, `chore:`.
