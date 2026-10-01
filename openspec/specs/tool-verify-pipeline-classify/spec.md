# tool-verify-pipeline-classify Specification

## Purpose
`verify_pipeline_classify` classifies the log text of a failed CI check into one of seven root-cause categories. The `verify-pipeline` skill calls it after a CI failure is observed. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input and output fields
The tool SHALL accept the input fields and return the output fields listed below.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `logs` | string | yes | plain text, e.g. `npm ERR! code E404` | Log text of the failed check to classify |
| `check_name` | string | no | plain text, e.g. `build` | Name of the failed check; echoed back |
| `conclusion` | string | no | plain text, e.g. `failure`, `timed_out` | Conclusion of the failed check; echoed back |

| Field | Meaning |
|---|---|
| `check_name` | Input `check_name`, unchanged; omitted when empty |
| `conclusion` | Input `conclusion`, unchanged; omitted when empty |
| `category` | One of `lint`, `test-failure`, `type-error`, `build-error`, `dependency`, `infra`, `unknown` |
| `signals` | Every matched pattern, as `<prefix>:<pattern>`; an empty list when nothing matched |

#### Scenario: Passthrough fields are echoed
- **WHEN** the tool is called with `logs: "npm ERR! code E404"`, `check_name: "build"`, `conclusion: "failure"`
- **THEN** the result has `check_name: build` and `conclusion: failure`
- **AND** `category` is `dependency`

#### Scenario: Passthrough fields do not change the category
- **WHEN** the same `logs` value is sent with and without `check_name` and `conclusion`
- **THEN** `category` and `signals` are the same in both results

### Requirement: Empty log text
The tool SHALL return `category: unknown` and an empty `signals` list when `logs` is empty or only whitespace.

#### Scenario: Empty logs
- **WHEN** the tool is called with `logs: ""`
- **THEN** `category` is `unknown`
- **AND** `signals` is an empty list

#### Scenario: Whitespace-only logs
- **WHEN** the tool is called with `logs: "   \n\t  "`
- **THEN** `category` is `unknown`

### Requirement: Signal patterns
The tool SHALL test the log text against every pattern in all six groups below and add one signal per matching pattern.

- A signal string is the group prefix, a colon, then the pattern text, e.g. `dep:\bnpm\s+ERR!\s+code\s+E\w+`.
- Signals come in group order: `lint`, `test`, `type`, `build`, `dep`, `infra`.
- Matching is case-insensitive unless the row says "case-sensitive".

| Prefix | Category | Matches (plain words) |
|---|---|---|
| `lint` | `lint` | `eslint`, `prettier`, `rubocop`, `golangci-lint`, `flake8`, `pylint` (whole words); `problems (N errors, N warnings)` |
| `test` | `test-failure` | `N failing`; `AssertionError` (case-sensitive); `expected ... received`; `FAIL <path>.test.js` / `.spec.ts` and jsx/tsx (case-sensitive); `Tests: N failed`; a line starting `FAILED tests` (case-sensitive); `pytest: ... failed` |
| `type` | `type-error` | `TS` + 4 digits, e.g. `TS2322` (case-sensitive); `Type '...' is not assignable`; `Property '...' does not exist on type`; `mypy`; `tsc ... error` |
| `build` | `build-error` | `Cannot find module`; `Module not found`; `SyntaxError:` (case-sensitive); `webpack N errors`; `rollup failed`; `esbuild ... error` |
| `dep` | `dependency` | `npm ERR! code E...`; `ENOENT ... node_modules`; `peer dep`; `unable to resolve dependency`; `yarn install ... failed`; `pip install ... ERROR` |
| `infra` | `infra` | `Runner lost communication`; `timeout` / `time out` / `time-out`; `unable to access 'http(s)://`; `502 Bad Gateway`; `503 Service Unavailable`; `ExitCode: 143` (case-sensitive) |

#### Scenario: Exact dependency signal
- **WHEN** `logs` is `npm ERR! code E404`
- **THEN** `signals` contains `dep:\bnpm\s+ERR!\s+code\s+E\w+`

#### Scenario: Exact lint signal
- **WHEN** `logs` is `Running eslint now`
- **THEN** `signals` contains `lint:\beslint\b`

#### Scenario: Exact infra signal
- **WHEN** `logs` is `502 Bad Gateway`
- **THEN** `signals` contains `infra:\b502\s+Bad\s+Gateway\b`

#### Scenario: Signals from several groups are all kept
- **WHEN** `logs` is `eslint found problems\n3 failing tests`
- **THEN** `signals` holds at least one `lint:` signal and at least one `test:` signal

### Requirement: Category priority
The tool SHALL pick the category of the highest-priority group that has at least one signal, in the order `lint` > `test-failure` > `type-error` > `build-error` > `dependency` > `infra`, and `unknown` when no pattern matched.

#### Scenario: Lint wins over test failure
- **WHEN** `logs` is `eslint found problems\n3 failing tests`
- **THEN** `category` is `lint`

#### Scenario: One category per group
- **WHEN** `logs` is each of the values below
- **THEN** `category` is the value in the same row

| `logs` | `category` |
|---|---|
| `Running eslint...\n12 problems (10 errors, 2 warnings)` | `lint` |
| `3 failing\n  1) foo bar` | `test-failure` |
| `AssertionError: expected true to equal false` | `test-failure` |
| `src/index.ts(10,5): error TS2322: Type mismatch` | `type-error` |
| `Error: Cannot find module 'foo'` | `build-error` |
| `npm ERR! code E404` | `dependency` |
| `Error: The operation was canceled: timeout` | `infra` |
| `502 Bad Gateway` | `infra` |

#### Scenario: No pattern matches
- **WHEN** `logs` is `everything is fine, build succeeded`
- **THEN** `category` is `unknown`
- **AND** `signals` is an empty list

### Requirement: Pure, side-effect-free call
The tool SHALL return a classification for every call without reading files, running commands, or returning an error.

- Annotations: `Title: "Classify CI failure logs"`, `ReadOnly: true`, `Idempotent: true`, `OpenWorld: false`.
- It is not a polling tool: there is no `pending` status and no state file.

#### Scenario: Repeated call gives the same result
- **WHEN** the tool is called twice with the same `logs`
- **THEN** both results have the same `category` and `signals`
