# Recovering From Failures

Reference for the `execute` skill — Step 6 (RECOVER).

Maximum retries per task: **2**. After 2 failures on the same task, escalate to the user.

## Failure Classification

| Failure Category | How to Detect | Severity |
|---|---|---|
| Agent timeout / no return | Agent does not return or hangs indefinitely | Medium |
| Agent error output | Agent returns error message instead of deliverable | Medium |
| Incomplete implementation | Agent output doesn't match expected deliverable | Low–Medium |
| Unauthorized file modification | `git diff` shows changes outside the agent's allowed file list | High |
| Test failure (1–2 tests) | Test suite fails on a small number of tests | Medium |
| Test failure (3+ tests) | Test suite fails on many tests | High |
| Build failure | Build command returns non-zero exit code | High |
| Lint failure | Linter reports new violations | Low |
| File conflict between agents | Two agents in the same wave modified the same file | High |
| Partial batch failure | Batch agent reports some tasks SUCCESS, some tasks FAILED | Medium |
| Phantom success | Agent reports task complete, but `git diff --stat` shows no changes to expected files, or canary grep for the verification token fails | High |
| Permission prompt hang (bypass mode) | Agent times out or hangs despite `mode: "bypassPermissions"` being set | Medium |
| Agent status: NEEDS_CONTEXT | Agent completion checklist reports STATUS: NEEDS_CONTEXT | Low |
| Agent status: BLOCKED | Agent completion checklist reports STATUS: BLOCKED | Medium–High |
| Malformed completion checklist | Agent output is missing the COMPLETE:/VERIFY:/STATUS: block, or it cannot be parsed | Low |
| CONTEXT_OVERFLOW | Fewer per-task completions were received than tasks were dispatched, OR one or more dispatched task IDs never reported completion — detected by the main session comparing the completion checklists it collected against the manifest-known dispatched set | High |

## Recovery Strategies

### Agent timeout / error output
Re-dispatch with the same prompt plus this addition at the top:
```
RETRY: Previous attempt failed with the following error:
{error message or description of what happened}

Please complete the task fully.
```
Max 1 retry. If it fails again, escalate.

### Model escalation on retry

When re-dispatching a failed task, escalate the model one step up the chain:

```
haiku → sonnet → opus → user (escalate, do not retry further)
```

Rules:
- Model escalation counts as one of the 2 allowed retries — it is not a separate retry budget.
- Only escalate one step per retry. Do not jump from haiku directly to opus.
- Always add failure context to the retry prompt regardless of model change. A stronger model with the same bad prompt produces the same bad output.
- Always pass `mode: "bypassPermissions"` when re-dispatching. Model escalation changes the model, not the mode — both parameters must be set on every dispatch.
- Tasks already on `opus` that fail: re-dispatch once with failure context and the same `opus` model. If that fails, escalate to user immediately.

Add this line at the top of the retry prompt when escalating:
```
MODEL ESCALATED: This task previously failed on {previous-model}. You are now running on {new-model}. Previous failure: {brief description}.
```

### Incomplete implementation
Re-dispatch with specific feedback:
```
RETRY: Your previous output was incomplete. Missing:
{specific deliverable that was not produced}

What was produced:
{summary of what the agent did return}

Complete only the missing parts. Do not redo what was already done.
```
Max 1 retry.

### Unauthorized file modification
Do NOT retry the agent without fixing the prompt first.
1. Identify which unauthorized files were modified
2. Revert them: `git checkout -- <file>`
3. Re-examine the task — if it genuinely needs those files, update the allowed file list
4. Re-dispatch with the corrected file list and a warning:
   ```
   CONSTRAINT: You previously modified files outside your allowed list. Stay strictly within:
   {updated file list}
   ```

### Test failure (1–2 tests)
Fix inline in the main context. These are typically integration issues from combining wave outputs (wrong import path, missing export, interface mismatch). Don't re-dispatch an agent for a 2-line fix.

### Test failure (3+ tests)
Stop. Diagnose before proceeding.
1. Read the failure output carefully
2. Determine whether failures share a root cause (same module, same interface change) or are independent
3. **Same root cause**: fix in main context, then continue
4. **Independent failures**: stop and escalate — there may be a systemic issue with the wave output

### Build failure
Stop immediately. Build failures mean subsequent waves will produce invalid output based on a broken foundation.
1. Read the build error
2. Diagnose: missing import, type error from interface change, circular dependency, removed export
3. Fix in main context
4. Rebuild to confirm green before proceeding to the next wave

### Lint failure
Fix inline. Never block a wave progression on lint-only failures unless the project is configured to treat lint as a hard error (e.g., CI fails on lint). In that case, treat as a build failure.

### File conflict between agents
1. Identify which agents touched the same file and what each changed
2. Read the current state of the conflicted file
3. Manually merge the correct final state in the main context
4. Run the affected tests/build to verify the merge is correct
5. Do not re-dispatch agents for the conflict — merge it yourself

### Partial batch failure

When a batch agent reports mixed results (some tasks SUCCESS, some tasks FAILED):

1. Accept the succeeded tasks as final — do not re-run them
2. Extract each failed task from the batch into its own individual retry
3. Re-dispatch each failed task as a standalone agent with:
   - The single-task Agent Prompt Template (not the batch template)
   - Model escalated one step: haiku → sonnet
   - `mode: "bypassPermissions"` passed explicitly to the Agent tool
   - Failure context from the batch report added at the top of the prompt
4. Treat each extracted retry independently — it counts toward that task's 2-retry budget
5. If the extracted retry also fails, escalate to the user per the standard escalation protocol

Do not re-dispatch the entire batch — this risks re-applying changes from tasks that already succeeded.

### Phantom success

When an agent reports successful completion but `git diff --stat` shows no changes to the expected files (or the canary grep for the agent's verification token returns no matches):

1. **Do NOT trust the agent's output.** The task is incomplete regardless of what the agent reported.

2. **Diagnose the likely cause:**
   - Agent used bash `sed`, `awk`, Python, or a compiled program in `/tmp` to patch files instead of the Edit tool → the patch silently failed
   - Agent wrote to a wrong path (e.g., a copy in `/tmp`) instead of the actual file
   - Agent hallucinated completing the task without invoking any file-editing tool

3. **Re-dispatch with escalated model and explicit constraints:**
   ```
   RETRY: Previous attempt reported success, but git diff shows no changes to the expected files.
   Your edits did NOT persist. This usually means a method other than the Edit tool was used.

   MANDATORY: Use the Edit tool for every file modification. Do not use bash sed, awk, Python
   scripts, Go programs, or any other indirect method. Each change must use Edit directly.

   Complete the task from scratch — assume none of your previous work exists.
   ```
   Escalate model one step (haiku → sonnet → opus) and pass `mode: "bypassPermissions"` explicitly. This counts toward the 2-retry budget.

4. **After the retry, re-run filesystem verification.** Run `git diff --stat` and grep for the verification token. If still no changes, escalate to the user immediately — do not retry a third time. Include: "Agent reported success twice but produced no filesystem changes. Manual implementation required."

5. **For phantom success in batch agents:** Extract the phantom-success tasks from the batch. Re-dispatch each individually (not as a batch) with the constraints above and model escalation. Tasks that genuinely succeeded in the batch remain final — do not re-run them.

### Permission prompt hang (bypass mode)

When an agent times out or hangs indefinitely despite `mode: "bypassPermissions"` being set:

1. **Check first:** Ask the user to confirm whether a permission prompt is visible in their terminal. If so, they should respond to it — the agent will resume automatically.

2. **If no prompt is visible** (agent genuinely hung, not waiting on user input): treat as a standard timeout. Re-dispatch with failure context and one model escalation step.

### Agent status: NEEDS_CONTEXT

The agent cannot complete the task without additional information. Provide the missing context and re-dispatch:

```
CONTEXT PROVIDED: The following information you requested has been provided below.

[Include the specific missing information directly in the prompt body]

Complete the task with this additional context. Previous partial work (if any) is already in the filesystem — do not redo it.
```

This counts as one retry toward the 2-retry budget. If the agent reports NEEDS_CONTEXT again after context was provided, escalate to the user — the task specification may be fundamentally incomplete.

### Agent status: BLOCKED

The agent cannot complete the task. Assess the blocker before acting:

1. **Context problem** — the agent lacks information to proceed:
   Provide the missing context and re-dispatch with the same or escalated model. Counts as one retry.

2. **Capability problem** — the task requires more reasoning than the assigned model:
   Re-dispatch with an escalated model (haiku → sonnet → opus). Counts as one retry.

3. **Scope problem** — the task is too large for a single agent dispatch:
   Break the task into smaller sub-tasks. Each sub-task starts with a fresh retry budget.

4. **Plan problem** — the plan itself is incorrect or contradictory:
   Escalate to the user immediately — do not retry.

When re-dispatching a blocked task, add at the top of the retry prompt:
```
PREVIOUSLY BLOCKED: The previous agent could not complete this task because:
{blocker description from agent's completion checklist}

{Provide additional context, or describe how the task scope has been adjusted}
```

### Malformed or missing completion checklist

The agent returned output but the structured completion checklist block is absent or unparseable (cannot extract COMPLETE:/VERIFY:/STATUS: lines).

Treat as incomplete output. Re-dispatch once:
```
RETRY: Your previous output did not include the required completion checklist. You must end your response with this exact block:

```
COMPLETE: files_created=[list or none] files_modified=[list or none] tests_added=[yes|no|n/a] tests_pass=[yes|no|n/a] build_pass=[yes|no|n/a]
VERIFY: <symbol_name> in <file_path>
STATUS: DONE | DONE_WITH_CONCERNS | NEEDS_CONTEXT | BLOCKED
```

If your previous work is already complete, verify it persists in the filesystem and re-report with the checklist filled in.
```

This counts as one retry. If the checklist is still missing or malformed on retry, proceed using filesystem verification (git diff, canary check) alone — do not escalate purely due to a missing checklist.

## Escalation Protocol

When escalating to the user after 2 failed retries or a systemic failure, provide:

```
⚠️ Escalation Required

Task: {task name and wave number}
Failure: {clear description of what went wrong}
Attempts: {N retries attempted}
Models used:     {e.g., "haiku (attempt 1), sonnet (attempt 2)"}

Recovery tried:
{describe each recovery attempt and its outcome}

Specific question / decision needed:
{the exact clarification or decision required to proceed}

Other waves affected:
{yes — waves N, N+1 are blocked} or {no — other waves can proceed independently}
```

Wait for the user's response before proceeding with any affected waves.

**Error-to-GitHub issue proposal**:

After escalating, also offer to track the failure as a GitHub issue. Locate the procedure:
Glob for `**/error-report/REFERENCE.md` under `~/.claude/plugins`, then retry with cwd.
If found, follow the procedure with:

- **Skill**: execute
- **Step**: Step 6 — RECOVER (Escalation)
- **Operation**: Task execution (task name and wave from escalation output above)
- **Error**: Persistent failure after 2 retries (details from escalation output above)
- **Suggested investigation**: Review the task description for ambiguity; check whether the task's allowed file list is complete; inspect agent error output for root cause

If not found, skip — the capability is not installed.

### CONTEXT_OVERFLOW

**Detection signal:** fewer per-task completions were received than tasks were dispatched, OR one or more dispatched task IDs are absent from the completions the main session collected. The main session derives this by comparing the dispatched task ID set against the IDs it actually received a completion checklist for (`missingIds` = dispatched minus received).

**Critical constraint:** `git diff --stat` output is NOT a substitute for missing per-task return data on CONTEXT_OVERFLOW. The wave may have partially written files while the agent context truncated before reporting. Do not treat a non-empty diff as evidence that all dispatched tasks completed successfully.

**Recovery — auto-split-and-retry:**

1. Call `execute_state({ action: "wave-split", dispatched: "<JSON array string of the ORIGINAL dispatched task IDs>", missingIds: "<JSON array string of the missing task IDs>", splitDepth: <N>, maxSplitDepth: 3 })` to partition the ORIGINAL dispatched task set (not just the missing subset) into two halves. `dispatched` and `missingIds` are flat top-level fields — not nested under a `payload` object — and each must be a JSON-encoded array-of-strings *string* (e.g. `"[\"1\",\"2\",\"3\"]"`), not a native array; the tool unmarshals them internally and rejects anything that fails to parse as JSON.
2. Re-dispatch each half as a separate sub-wave, recomputing the byte budget (see [classifying-and-waving-tasks.md](classifying-and-waving-tasks.md)'s Adaptive Wave Size Cap) before dispatch.
3. Each sub-wave starts with a fresh retry budget (consistent with the "scope problem" recovery precedent above).
4. If a sub-wave itself returns CONTEXT_OVERFLOW, recurse: call `execute_state({action:"wave-split"})` again with incremented `splitDepth`.
5. **Maximum split depth: 3.** If the call fails with `MaxSplitDepthExceededError`, do NOT split further. Escalate to the user with the full structured missing-IDs error: `{missingIds, totalPlanned, totalAccounted}`.

**Why split the full dispatched set (not just missing IDs):**
Splitting only the missing IDs risks re-overflowing if the dependencies or shared context for those tasks require the full set to be present in the wave. Splitting the original set guarantees each half fits in the model's context.

**Maximum splits:** 3 depths per original wave. The max-depth ceiling prevents runaway recursive splitting when a single large task alone exceeds the byte budget (in that case, escalate to user rather than splitting infinitely).

## Rollback Strategy

When a wave produces fundamentally broken output that cannot be recovered through targeted fixes:

1. Stash all changes from the failed wave:
   ```bash
   git stash push -m "failed-wave-N-$(date +%Y%m%d-%H%M%S)"
   ```

2. Confirm the stash captured everything:
   ```bash
   git status
   git stash list
   ```

3. Re-examine the tasks in the failed wave. Common root causes:
   - Task descriptions were too vague (agents interpreted them differently)
   - Dependencies on prior wave outputs were incorrect
   - Tasks had hidden conflicts not caught by the file-conflict check

4. Present the user with options:
   - **Retry the wave** with revised task descriptions
   - **Skip the wave** and mark tasks as unimplemented (user completes manually)
   - **Abort execution** entirely (stash remains, user takes over)

5. If retrying: pop the stash only after the new wave succeeds:
   ```bash
   git stash drop stash@{0}
   ```
   If aborting: leave the stash for the user to inspect.
