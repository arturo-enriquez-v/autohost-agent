# Contributing to AutoHost

Thank you for your interest in contributing to **AutoHost**! We welcome bug reports, improvements, documentation updates, and feature requests.

---

## Code of Conduct & Expectations

* Be respectful and constructive in issues, discussions, and pull requests.
* Keep pull requests focused on a single concern or fix.
* Write clear commit messages and PR descriptions.

---

## Contributor License Agreement (CLA)

> [!IMPORTANT]
> All external contributors must agree to the **[AutoHost Contributor License Agreement](CLA.md)** before any pull request can be merged.

### Why is this needed?
AutoHost is distributed under the [GNU Affero General Public License v3.0 (AGPLv3)](LICENSE) license for community use, and the project maintainers offer commercial licenses and services. The CLA ensures that:
1. You retain the copyright to your original work.
2. The project maintainers receive the necessary rights to distribute, maintain, and dual-license your contributions as part of both the free Community Edition and future commercial releases without legal conflicts.

### How to sign
When you open a Pull Request, an automated bot (**CLA Assistant**) will post a comment if you have not signed yet. Simply click the link and authenticate with your GitHub account to accept the CLA.

---

## How to Contribute

### 1. Opening an Issue
Before submitting large changes or new features, please open an Issue to discuss the proposal with the maintainers.

### 2. Developing Changes
1. Fork the repository and create a branch from `main`:
   ```bash
   git checkout -b fix/your-bug-fix
   # or
   git checkout -b feat/your-feature
   ```
2. Make your changes adhering to existing code conventions.
3. Verify that tests pass and the project builds cleanly:
   ```bash
   make dev
   # or
   go test ./...
   ```

### 3. Submitting a Pull Request
1. Push your branch to your fork.
2. Open a Pull Request against the `main` branch.
3. Fill in the PR description template clearly explaining:
   * What changed
   * Why it was needed
   * How it was tested
4. Complete the CLA check if prompted.

---

## License

By contributing to AutoHost, you agree that your contributions will be licensed in accordance with [CLA.md](CLA.md) and the project's [LICENSE](LICENSE).
