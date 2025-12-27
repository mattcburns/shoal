# Agent Protocol: Shoal Repository

3.  **Ensure All Tests Pass:** Before you consider your task complete, you MUST run the full validation pipeline and ensure all tests pass.
    ```bash
    go run build.go validate
    ```y

**ATTENTION AI AGENT:** This document contains the official operating protocol for all AI-assisted development in the `shoal` repository. You are required to read, understand, and strictly adhere to these rules at all times. Failure to comply with these directives will result in corrective action.

## 1. CRITICAL: Non-Negotiable Core Directives

These are the most important rules. They are not suggestions. You MUST follow them for every task, without exception.

### 1.1. Git Workflow: ALWAYS Use Feature Branches

**No work is permitted on the `master` branch.** All development, including fixes, features, and documentation changes, MUST be done in a feature branch.

1.  If we are already on a branch other than `master` then you do not need to create an additional feature branch. We
    don't want to create branches upon branches for changes.

2.  **ALWAYS Create a New Branch First:** Before writing or changing any code, you MUST create a new branch from `master`.
    ```bash
    git checkout -b <branch_name>
    ```

3.  **Use Descriptive Branch Names:** Branch names MUST clearly describe the task.
    -   **Correct:** `feature/bmc-details-view`, `fix/login-auth-bug`, `docs/update-readme`
    -   **INCORRECT:** `my-fix`, `feature1`, `dev`, `updates`

### 1.2. Testing: ALWAYS Write and Pass Tests

**Code without tests is incomplete.** Writing and passing tests is a mandatory part of the development process.

1.  **Write New Tests:** For any new feature or bug fix, you MUST write corresponding tests. This is not optional.
2.  **Ensure All Tests Pass:** Before you consider your task complete, you MUST run the full validation pipeline and ensure all tests pass.
    ```bash
    python3 build.py validate
    ```
3.  **Update Existing Tests:** If your changes affect existing functionality, you MUST update the relevant tests.

### 1.3. Documentation: ALWAYS Update Documentation

**Documentation must be kept current.** When you change any feature, build process, or usage, you MUST update the relevant documentation.

-   **`README.md`**: For human developers. Update with user-facing changes.
-   **`DEPLOYMENT.md`**: For deployment and configuration changes.
-   **`AGENTS.md`**: This file. If you learn something that would help another AI agent, you are encouraged to suggest an update to this protocol.

### 1.4. Source File Licensing: ALWAYS Include License Header

**All new source files MUST include the standard AGPLv3 license header.** This is a mandatory requirement to maintain license compliance.

1.  **Apply to All New Files:** Every new `.go`, `.py`, `.js`, `.css`, or other source file must begin with the license header.
2.  **Use Correct Copyright:** The copyright line should be `Copyright (C) <year> <name of author>`. For this project, the author is `Matthew Burns`.
3.  **Use Standard Template:** The header format depends on the file type.

    **For Go (`.go`):**
    ```go
    // Shoal is a Redfish aggregator service.
    // Copyright (C) <year> <name of author>
    //
    // This program is free software: you can redistribute it and/or modify
    // it under the terms of the GNU Affero General Public License as published by
    // the Free Software Foundation, either version 3 of the License, or
    // (at your option) any later version.
    //
    // This program is distributed in the hope that it will be useful,
    // but WITHOUT ANY WARRANTY; without even the implied warranty of
    // MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    // GNU Affero General Public License for more details.
    //
    // You should have received a copy of the GNU Affero General Public License
    // along with this program.  If not, see <https://www.gnu.org/licenses/>.
    ```

    **For Python (`.py`):**
    ```python
    # Shoal is a Redfish aggregator service.
    # Copyright (C) <year> <name of author>
    #
    # This program is free software: you can redistribute it and/or modify
    # it under the terms of the GNU Affero General Public License as published by
    # the Free Software Foundation, either version 3 of the License, or
    # (at your option) any later version.
    #
    # This program is distributed in the hope that it will be useful,
    # but WITHOUT ANY WARRANTY; without even the implied warranty of
    # MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    # GNU Affero General Public License for more details.
    #
    # You should have received a copy of the GNU Affero General Public License
    # along with this program.  If not, see <https://www.gnu.org/licenses/>.
    ```

    **For CSS/JS (`.css`, `.js`):**
    ```javascript
    /*
    Shoal is a Redfish aggregator service.
    Copyright (C) <year> <name of author>

    This program is free software: you can redistribute it and/or modify
    it under the terms of the GNU Affero General Public License as published by
    the Free Software Foundation, either version 3 of the License, or
    (at your option) any later version.

    This program is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    GNU Affero General Public License for more details.

    You should have received a copy of the GNU Affero General Public License
    along with this program.  If not, see <https://www.gnu.org/licenses/>.
    */
    ```

## 2. Project Overview for AI Agents

### 2.1. Purpose

Shoal is a Redfish aggregator service. Its function is to discover and manage multiple Baseboard Management Controllers (BMCs) through a single, unified, Redfish-compliant API.

### 2.2. Architecture

-   **Presentation Layer**: Web UI (`internal/web`) and REST API (`internal/api`).
-   **Business Logic Layer**: BMC management (`internal/bmc`) and authentication (`internal/auth`).
-   **Data Access Layer**: SQLite database operations (`internal/database`).
-   **Infrastructure Layer**: Logging (`internal/logging`), Models (`pkg/models`).

Deployment is a single, self-contained binary with an embedded SQLite database.

### 2.3. License and Dependencies

**CRITICAL: All new dependencies MUST be compatible with AGPLv3.**

-   **Project License**: GNU Affero General Public License v3.0 (AGPLv3).
-   **Compatible Licenses**: MIT, Apache 2.0, BSD, ISC.
-   **Incompatible Licenses**: Any license more restrictive than AGPLv3.

You MUST verify the license of any new dependency before adding it.

## 3. Mandatory Development Workflow & Tools

This section outlines the commands you will use for development. These are not just informational; this is the required workflow.

### 3.1. The Primary Tool: `build.go`

All build, test, and validation tasks are executed through the Go-based automation script `build.go`. You can run it with `go run build.go`.

### 3.2. Standard Development Commands

-   **`go run build.go validate`**: **This is the main command for development.** It runs the full validation pipeline (format, lint, test, build). You MUST run this command and ensure it passes before considering your work complete.
-   **`go run build.go test`**: Runs the test suite.
-   **`go run build.go coverage`**: Runs tests and generates an HTML coverage report. Use this to verify your new tests are increasing coverage.
-   **`go run build.go build`**: Compiles the Go binary.
-   **`go run build.go fmt`**: Formats all Go code.
-   **`go run build.go lint`**: Runs static analysis.

### 3.3. Running the Application

1.  First, build the binary:
    ```bash
    go run build.go build
    ```
2.  Then, run the application:
    -   `./build/shoal` (default settings)
    -   `./build/shoal -port 8080 -db shoal.db -log-level debug` (custom settings)

## 4. Task-Specific Protocols

-   **Security:** Always sanitize inputs and validate data.
-   **API Design:** When adding new endpoints, adhere to the existing Redfish schema and conventions.
-   **Simplicity:** Prefer the standard library over new dependencies. Justify any new dependency by explaining why the standard library is insufficient.

### 4.1 Dead Code Scans

We periodically run a dead code scan to keep the exported surface minimal and avoid accumulating unused helpers.

Run the scan:
```bash
$HOME/go/bin/deadcode ./...
```
If the `deadcode` binary is not installed:
```bash
go install golang.org/x/tools/cmd/deadcode@latest
$HOME/go/bin/deadcode ./...
```

Interpretation notes:
1. The tool does not treat the `main` package entry point as a root, so constructors like `internal/database.New` can appear as unreachable even though they are used via `cmd/shoal/main.go` and tests. Treat these as false positives unless truly unused.
2. Functions referenced only from `_test.go` files are reported as unreachable. Decide case‑by‑case whether to (a) keep (test utility / future roadmap) or (b) remove / unexport.
3. Before deleting any reported symbol, perform a repo search to ensure no reflective / template / JSON tag usage depends on the name indirectly.

Current allowlist (intentionally retained though flagged):
- `internal/database.New` (constructor used by main & tests; false positive)
- `(*DB).GetSettingsDescriptors` (used indirectly by settings discovery tests; core to settings caching)
- `(*DB).CleanupExpiredSessions` (future scheduled maintenance task; exercised in tests)
- `(*DB).DisableForeignKeys` (test helper to simplify setup)
- `(*DB).UpdateConnectionMethodAggregatedData` (future aggregation caching; covered by tests)
- `(*DB).UpdateConnectionMethodLastSeen` (future monitoring feature; covered by tests)
- `pkg/auth.isHashed` (intentionally unexported helper, only used in password tests)
- `(*DB).UpsertConsoleCapability` (console pass-through feature Milestone 1; design/021; covered by tests)
- `(*DB).GetConsoleCapabilities` (console pass-through feature Milestone 3; design/021; covered by tests)
- `(*DB).GetConsoleCapability` (console pass-through feature Milestone 3; design/021; covered by tests)
- `(*DB).CreateConsoleSession` (console pass-through feature Milestone 3; design/021; covered by tests)
- `(*DB).GetConsoleSession` (console pass-through feature Milestone 4; design/021; covered by tests)
- `(*DB).GetConsoleSessions` (console pass-through feature Milestone 3; design/021; covered by tests)
- `(*DB).UpdateConsoleSessionState` (console pass-through feature Milestone 4; design/021; covered by tests)
- `(*DB).DeleteConsoleSession` (console pass-through feature Milestone 4; design/021; covered by tests)
- `(*Service).SyncConsoleCapabilities` (console pass-through feature Milestone 2; design/021; covered by tests)

Removal / retention guidelines:
- Remove immediately if: no test coverage, no design doc reference, and not part of an accepted roadmap item.
- Unexport (rename to lowercase) if: only tests need it and semantics are simple.
- Keep & document if: there is an existing design document or near‑term feature referencing it (add a NOTE comment above the function, as done for the DB helpers).

When updating this allowlist:
1. Trim items that gain real production call sites (they will disappear from the deadcode output).
2. Add justification inline so future agents do not reintroduce churn.
3. Re-run `go run build.go test` after any removals.

PR checklist for dead code sweeps:
- [ ] Run deadcode tool
- [ ] Classify each item (remove / unexport / keep+doc)
- [ ] Update or confirm allowlist section
- [ ] Ensure tests still pass (`go run build.go validate`)
- [ ] Summarize changes in PR description referencing this section
