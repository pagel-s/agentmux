# Repository working agreements

- The owner wants completed, validated changes committed and pushed to `origin`.
  This is standing authorization unless the current task says otherwise. Preserve
  unrelated work, use normal fast-forward pushes, and never force-push or publish
  credentials. Report the commit and remote verification when finished.
- The owner permits Codex, Antigravity with an explicit Gemini model, and Claude
  for live agent or workflow validation. Choose provider profiles explicitly,
  bound native calls, and retain their validation evidence. Model-free fixtures
  remain the default for automated regression and soak tests.
- Run checks appropriate to the change. Required isolation must fail closed;
  uncertainty, skipped live validation and operational boundaries must remain
  visible. Passing tests do not justify claims of universal correctness.
- Every independent review must start with fresh context. Spawn review agents
  with `fork_turns="none"` and provide only the task brief, acceptance criteria
  and current code/diff. Do not include implementation chat, prior review
  conclusions or approvals. Use a new reviewer context for each review round;
  implementation feedback does not count as independent approval.
