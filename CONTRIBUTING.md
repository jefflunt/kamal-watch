# Contributing to Kamal Watch

Thank you for your interest in contributing to Kamal Watch! Whether you are a human developer or an AI coding agent, we welcome high-quality issues and pull requests.

---

## Guidelines for AI Agents & Automated Contributors

If you are an AI assistant, coding agent (e.g., Antigravity, Claude Code, Cursor, Copilot, SWE-bench agent), or automated bot contributing to this repository, please observe the following requirements:

1. **Precision & Grounding Over Speculation**:
   - Always base issues and pull requests on verifiable code paths and actual error reproductions.
   - Reference exact file paths, line ranges (e.g., `pkg/remote/ssh.go#L74`), and concrete configurations.
   - Do not hallucinate dependencies, non-existent flags, or speculative architecture changes.

2. **Run Tests & Verify Locally**:
   - Before submitting a pull request, run all unit tests:
     ```bash
     go test -v ./...
     ```
   - Verify code formatting and linting:
     ```bash
     go vet ./...
     ```
   - Build the binary to verify clean compilation:
     ```bash
     ./cmd/build
     ```

3. **Respect Security & Secrets Boundaries**:
   - Never commit sensitive infrastructure data, IP addresses, credentials, private keys, or certificates.
   - Honor `.gitignore` and `.dockerignore`. Local configs (`kamal-watch.yml`, `.kamal/`, `config/deploy*.yml`) must never be added to Git tracking.

4. **Use Established Templates**:
   - When filing issues, use the provided `.github/ISSUE_TEMPLATE/` issue templates (`bug_report.md` or `feature_request.md`).
   - When filing PRs, include a concise summary of changes, motivation, test coverage, and any breaking behavioral implications.

5. **Concise, High-Signal Descriptions**:
   - Avoid generic AI fluff or verbose boilerplate. Focus strictly on:
     - Root cause analysis / problem statement
     - Proposed solution or architecture change
     - Test verification steps

---

## Development Setup

1. **Prerequisites**:
   - Go 1.22+ (tested with Go 1.26)
   - Docker (optional, for containerized builds and end-to-end tests)

2. **Building from Source**:
   ```bash
   go build -o bin/kamal-watch ./cmd/kamal-watch
   # or
   ./cmd/build
   ```

3. **Running the Test Suite**:
   ```bash
   go test -v ./...
   ```

4. **Submitting Changes**:
   - Fork the repository.
   - Create a feature branch: `git checkout -b feat/your-feature`.
   - Commit your changes with clear, descriptive commit messages.
   - Submit a pull request against `main`.
