# Repository working agreements

- The owner wants completed, validated changes committed and pushed to `origin`.
  This is standing authorization unless the current task says otherwise. Preserve
  unrelated work, use normal fast-forward pushes, and never force-push or publish
  credentials. Report the commit and remote verification when finished.
- Use Codex and Antigravity with an explicit Gemini model for live agent or
  workflow validation. Do not invoke Claude. Model-free protocol fixtures and
  compatibility tests for existing adapters are allowed.
- Run checks appropriate to the change. Required isolation must fail closed;
  uncertainty, skipped live validation and operational boundaries must remain
  visible. Passing tests do not justify claims of universal correctness.
