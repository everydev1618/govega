---
name: debugging
description: Systematic debugging methodology and root cause analysis
tags: [debug, troubleshoot, error, bug]
tools: [read_file, list_files]
triggers:
  - type: keyword
    keywords: [debug, bug, error, crash, fix, broken, failing, stacktrace, traceback]
  - type: pattern
    pattern: "(debug|fix|diagnose|troubleshoot) (this|the|my|a)"
---

# Debugging Expert

When debugging issues, follow this systematic approach:

## 1. Reproduce
- Identify the exact steps to reproduce the issue
- Note the expected vs actual behavior
- Determine if it's consistent or intermittent

## 2. Isolate
- Narrow down the affected component or module
- Check recent changes (git log, git diff)
- Identify the minimal reproduction case

## 3. Analyze
- Read error messages and stack traces carefully
- Check logs at the time of failure
- Trace data flow through the affected code path
- Look for common causes:
  - Null/nil references
  - Off-by-one errors
  - Race conditions
  - Resource leaks
  - Type mismatches
  - Missing error handling

## 4. Hypothesize and Test
- Form a hypothesis about the root cause
- Design a test to confirm or deny it
- Make one change at a time
- Verify the fix doesn't introduce regressions

## 5. Fix and Verify
- Apply the minimal fix for the root cause
- Add a test that catches this specific bug
- Check for similar patterns elsewhere in the codebase
- Document the root cause if non-obvious

## Common Debugging Strategies

- **Binary search**: Comment out half the code to isolate the issue
- **Rubber duck**: Explain the problem step by step
- **Print debugging**: Add strategic log statements
- **Reversal**: Start from the error and work backwards
- **Fresh eyes**: Re-read the code as if seeing it for the first time
