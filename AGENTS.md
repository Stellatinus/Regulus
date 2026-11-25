# Notes for AI coding agents

Everything an agent needs to work on Regulus is in this repository. Read these before starting:

- [README.md](README.md): what Regulus is. The [documentation index](docs/README.md) lists every other page (installation, configuration, updates, console, authentication, remote agents, API, dashboard).
- [CONTRIBUTING.md](CONTRIBUTING.md): setup, commands, branches, commits, language.
- [docs/architecture.md](docs/architecture.md): how hub, agents, trust, data and updates fit together, with diagrams.
- [docs/glossary.md](docs/glossary.md): what each term means today; add new terms here.
- [docs/hub-redundancy.md](docs/hub-redundancy.md): the guide to the result of epic #103; a design issue of #103 is not complete until its section is here.
- [docs/versioning.md](docs/versioning.md): the contracts between versions (agent protocol, the frozen update contract, helper files); read it before touching `proto/`.
- [docs/decisions.md](docs/decisions.md): why things are the way they are, what is still open, what has not been verified on a real machine.
- [docs/plugins/](docs/plugins/README.md): the plugin system (milestone M6): manifest, runtime, writing a plugin, and what is still design only.

If you learn something that matters for future work and it is not written in one of these files, add it to the right one in the same pull request. Do not rely on context outside the repository.

## How to work

- **One pull request per issue**, referenced with `Closes #N`. Follow CONTRIBUTING.md for branches, Conventional Commits and language (English only for code, comments, commits, issues, pull requests and documents; only the GUI is bilingual, English and Italian).
- **Branches are for development and for stable versions** (the maintainer's decision, 2026-11-25). Work happens on one branch per issue and goes into `main` by pull request (the branch a cloud session is given counts as that). When a stable version such as `1.0.0` is released, a branch for it is kept (`release/1.0.0`) and development goes on in `main`; no stable release exists yet. Do not create other long-lived branches, and do not push to, rewrite or delete a release branch. The alphas and betas published so far are removed by the maintainer once a stable version exists (`docs/releases.md`, `docs/decisions.md`, "Releases and channels"); never delete a release or a tag yourself.
- **Checks:** run `make lint test` (and `make proto-check` after touching `proto/`) before pushing. CI runs on every pull request and must be green; do not add `[skip ci]` to commits.
- **Package comments:** every package under `internal/` and `cmd/` has a comment that says what it does and what it does not. `golangci-lint` fails when one is missing; keeping it true when you change the package is your job (CI cannot check that).
- **Decisions:** when a pull request makes or changes a design decision, update `docs/decisions.md` in the same pull request. If code and a document disagree, say so and fix the one that is wrong, or open an issue if it needs a decision.
- **New areas start with a design issue.** For a feature that is not yet designed (for example inventory or AI Support), write the design in an issue first and wait for agreement before writing code.
- **`[Test]` issues:** when a change needs a check on a real machine, open an issue titled `[Test] …` (label `test`) that says what is needed, the steps, the expected result and what to report back (see CONTRIBUTING.md), and say in the pull request that this part is not verified.
- **Do not overstate verification.** Say what was tested and how. Parts listed in #81 have not run on a real system yet; do not describe them as verified.

## Reporting back

At the end of a piece of work, report:

1. a **completeness score from 0 to 100** for the area you worked on, with the list of what is missing to reach 100;
2. an **issue for each gap** you found (in English, with a "Done when" line), unless one already exists;
3. the **milestone** you chose for each new issue and why (see the table in `docs/decisions.md`).
