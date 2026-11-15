## MODIFIED Requirements

### Requirement: Remote review verdict
For `target: remote_review` the tool SHALL read the PR's reviews and return a verdict for the first configured reviewer, in `reviewers` order, whose latest submitted review maps to a verdict.

| Latest review state | Copilot overview header in the review body | `ext.verdict` |
|---|---|---|
| `CHANGES_REQUESTED` | any | `actionable` |
| `APPROVED` or `COMMENTED` | `🟢 Approval recommended`, with no open and no previously missed item | `approved-clean` |
| `APPROVED` or `COMMENTED` | `🟢 Approval recommended` with an open or previously missed item, `🟡 Changes recommended`, or `🔵 Needs a closer look` | `actionable` |
| `APPROVED` | none | `approved-clean` |
| `COMMENTED` | none | `actionable` |
| `DISMISSED` or no review | — | none; keep polling |

- Login match is case-insensitive and exact (not a substring); a trailing `[bot]` is ignored; `copilot-pull-request-reviewer` matches `copilot`.
- Reviews by logins not in `reviewers` are ignored. `PENDING` (draft) reviews are ignored.
- "Latest" is by `submittedAt`; without timestamps, the later entry in gh's order wins.
- Blank `reviewers` entries are skipped.
- The overview header is an H3 line of the review body: `### 🟢 Approval recommended`, `### 🟡 Changes recommended` or `### 🔵 Needs a closer look`. The `<!-- ccr-overview-v2 -->` marker is not required.
- Open items are the items of the `N open issue(s)` block. Previously missed items are the items of the `N previously missed issue(s)` block.
- `done` ext: `verdict`, `reviewer` (the configured name as passed), `state` (gh's raw state), `overview`, `pr_number`.
- `overview` is `approval-recommended`, `changes-recommended`, `needs-closer-look`, or `""` when the latest review body of that reviewer has no header. A header of another reviewer's review never sets it.
- `pending` ext: `pr_number`, `reviewers`.
- Empty gh output means no reviews (pending). Output that is not valid JSON is a probe failure.

#### Scenario: Copilot bot approval
- **WHEN** `reviewers` is omitted and gh reports login `copilot-pull-request-reviewer[bot]` with state `APPROVED`
- **THEN** `ext.verdict` is `approved-clean` and `ext.reviewer` is `copilot`

#### Scenario: Case-insensitive reviewer keeps the configured spelling
- **WHEN** `reviewers` is `["ALICE"]` and gh reports login `alice` with state `COMMENTED`
- **THEN** `ext.verdict` is `actionable` and `ext.reviewer` is `ALICE`

#### Scenario: Unconfigured reviewer is ignored
- **WHEN** only login `mallory` has approved and `reviewers` is the default
- **THEN** `status` is `pending`

#### Scenario: Dismissed latest review
- **WHEN** reviewer `alice` has a `COMMENTED` review followed by a later `DISMISSED` review
- **THEN** there is no verdict and the poll stays `pending`

#### Scenario: Malformed gh output
- **WHEN** gh prints `not json`
- **THEN** `status` is `error`

#### Scenario: Copilot recommends approval with no finding
- **WHEN** the latest Copilot review has state `COMMENTED`
- **AND** its body has `### 🟢 Approval recommended` and no open or previously missed item
- **THEN** `ext.verdict` is `approved-clean`
- **AND** `ext.overview` is `approval-recommended`

#### Scenario: Copilot recommends approval with a missed finding
- **WHEN** the latest Copilot review has state `COMMENTED`
- **AND** its body has `### 🟢 Approval recommended` and `1 previously missed issue`
- **THEN** `ext.verdict` is `actionable`

#### Scenario: Copilot needs a closer look
- **WHEN** the latest Copilot review has state `COMMENTED` and the header `### 🔵 Needs a closer look`
- **THEN** `ext.verdict` is `actionable`
- **AND** `ext.overview` is `needs-closer-look`

#### Scenario: Header wins over an approval
- **WHEN** the latest Copilot review has state `APPROVED` and the header `### 🟡 Changes recommended`
- **THEN** `ext.verdict` is `actionable`

#### Scenario: Dismissed header review of another reviewer
- **WHEN** `reviewers` is `["copilot", "alice"]`
- **AND** Copilot's only review is `DISMISSED` with a 🔵 header and `alice` has a `COMMENTED` review with no header
- **THEN** `ext.verdict` is `actionable` and `ext.reviewer` is `alice`
- **AND** `ext.overview` is `""`
