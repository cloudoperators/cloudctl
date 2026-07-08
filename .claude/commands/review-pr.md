# review-pr

Review an open pull request, assess every comment for validity, implement valid fixes (with approval), reply to all threads, resolve them, and — when Copilot is the reviewer — iterate until Copilot raises no further comments.

## Usage

```
/review-pr <PR-number-or-URL>
```

## What this command does

This command runs as a **loop**:

1. **Fetch comments** — retrieve all inline review comments and conversation comments from the PR using `gh api`.

2. **Assess each comment** — for every comment decide:
   - **Valid**: the comment identifies a real bug, correctness issue, or meaningful improvement in the code *as it exists in the PR diff*.
   - **Stale/inapplicable**: the comment refers to a line that no longer exists or a condition that the PR has already addressed.
   - **Invalid/disagree**: the suggested change would be incorrect, harmful, or violates project conventions.

3. **Present findings** — explain each comment and your assessment to the user before touching any code. Get explicit approval to proceed.

4. **Implement valid fixes** — for approved valid comments:
   - Read the relevant files first.
   - Apply the minimal change that addresses the comment.
   - Run `make test` to verify nothing is broken.
   - Commit with a DCO sign-off (`git commit -s`) following the `type(scope): description` style from `AGENTS.md`.
   - Push to the PR branch.

5. **Reply to every comment** — after each fix is pushed, post a reply to the original thread referencing the commit SHA. For stale or invalid comments, post a factual explanation of why no code change was made.

6. **Resolve every thread** — mark all comment threads as resolved via the GraphQL API.

7. **Re-request review if Copilot was the reviewer** — if `copilot-pull-request-reviewer[bot]` was among the reviewers, re-request its review so it re-evaluates the updated code.

8. **Wait and iterate** — poll the PR for new comments. Once Copilot posts its next review, go back to step 1. The loop exits when either:
   - All threads are resolved and Copilot posts no new comments, **or**
   - Copilot was not a reviewer (no loop needed).

---

## Steps in detail

### 1 — Gather PR data

Run all three commands in parallel:

```bash
gh pr view <PR> --repo cloudoperators/cloudctl --json title,body,comments,reviews,url
gh api repos/cloudoperators/cloudctl/pulls/<PR>/reviews
gh api repos/cloudoperators/cloudctl/pulls/<PR>/comments
```

Also read the current branch to understand what the PR has already changed:

```bash
git diff main...HEAD -- <changed-files>
```

### 2 — Assess comments

For each **unresolved** comment thread:
- Quote the comment body.
- Show the diff hunk it points at.
- State: **Valid** / **Stale** / **Invalid** with a one-paragraph rationale.

Skip threads that are already resolved (`isResolved: true`).

### 3 — Get approval

Present the full assessment and ask the user whether to proceed before making any change.

### 4 — Implement fixes

For each approved valid fix:

1. Read the full file (`Read` tool).
2. Apply the change (`Edit` tool — prefer targeted edits over full rewrites).
3. Run tests: `make test`.
4. If tests pass, commit and push:

```bash
git add <files>
git commit -s -m "fix(scope): <description>"
git push origin <branch>
```

### 5 — Reply to every comment

For **valid comments** (after the fix is pushed), reply referencing the commit SHA:

```bash
gh api repos/cloudoperators/cloudctl/pulls/<PR>/comments \
  --method POST \
  --field in_reply_to=<comment-id> \
  --field body="Fixed in commit <sha>. <one-sentence summary of what changed.>"
```

For **stale or invalid comments**, reply explaining why no code change was made:

```bash
gh api repos/cloudoperators/cloudctl/pulls/<PR>/comments \
  --method POST \
  --field in_reply_to=<comment-id> \
  --field body="<factual explanation of why this is not actioned>"
```

Note: use `in_reply_to` (not a `/replies` sub-resource — that endpoint does not exist for PR review comments).

### 6 — Resolve every thread

After replying, resolve the thread via the GraphQL API (there is no REST endpoint for this):

```bash
# Step 1 — get the thread node IDs and their resolved state
gh api graphql -f query='
{
  repository(owner: "cloudoperators", name: "cloudctl") {
    pullRequest(number: <PR>) {
      reviewThreads(first: 50) {
        nodes {
          id
          isResolved
          comments(first: 1) {
            nodes { databaseId body }
          }
        }
      }
    }
  }
}'

# Step 2 — resolve each unresolved thread by its node ID
gh api graphql -f query='
mutation {
  resolveReviewThread(input: {threadId: "<THREAD_NODE_ID>"}) {
    thread { id isResolved }
  }
}'
```

### 7 — Re-request Copilot review (if applicable)

Check whether `copilot-pull-request-reviewer[bot]` appears in the reviewer list:

```bash
gh api repos/cloudoperators/cloudctl/pulls/<PR>/requested_reviewers
```

If Copilot was a reviewer, re-request its review so it re-evaluates the updated diff:

```bash
gh api repos/cloudoperators/cloudctl/pulls/<PR>/requested_reviewers \
  --method POST \
  --field "reviewers[]=" \
  --field "team_reviewers[]=copilot-pull-request-reviewer"
```

If the bot cannot be requested directly by name, pushing a new commit to the PR branch will trigger Copilot automatically in most configurations.

### 8 — Wait and poll for new comments

After re-requesting, poll until a new Copilot review appears:

```bash
# Poll every 30 seconds, up to 20 minutes
for i in $(seq 1 40); do
  NEW_COUNT=$(gh api repos/cloudoperators/cloudctl/pulls/<PR>/comments \
    --jq '[.[] | select(.user.login == "copilot-pull-request-reviewer[bot]")] | length')
  echo "Attempt $i: $NEW_COUNT Copilot comments found"
  [ "$NEW_COUNT" -gt "$PREVIOUS_COUNT" ] && break
  sleep 30
done
```

Once new comments appear, return to **step 1** with the new unresolved threads.

### Exit condition

The loop terminates when:
- All review threads are `isResolved: true`, **and**
- The latest Copilot review contains no new inline comments (review body only, or no review at all).

---

## Conventions (from AGENTS.md)

- All commits must be signed off (`-s`).
- Commit style: `type(scope): short description` — e.g. `fix(sync): handle empty kubeconfig context`.
- Do **not** add `Co-authored-by` trailers.
- Run `make fmt` before committing if you touched Go files.
- After any fix, run `make test` and confirm all tests pass.

---

## Example session

```
User:  /review-pr 61

--- Iteration 1 ---
Agent: Fetches comments. Finds 3 unresolved Copilot threads.
       Presents assessments (2 valid, 1 stale). Gets approval.
       Implements fixes, runs make test, commits 167822c, pushes.
       Replies to all 3 threads. Resolves all 3 threads.
       Detects Copilot was the reviewer → re-requests Copilot review.
       Polls for new comments...

--- Iteration 2 ---
Agent: New Copilot review detected (1 new comment).
       Presents assessment (1 valid). Gets approval.
       Implements fix, commits a2f91cc, pushes.
       Replies to thread. Resolves thread.
       Re-requests Copilot review. Polls for new comments...

--- Iteration 3 ---
Agent: Copilot review posted — no inline comments (summary only).
       All threads resolved. Loop complete.
       Informs user: PR is clean, no further Copilot comments.
```
