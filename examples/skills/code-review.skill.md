---
name: code-review
description: Expert guidance for reviewing code quality and best practices
tags: [code, review, quality]
tools: [read_file, list_files]
triggers:
  - type: keyword
    keywords: [review, code review, PR, pull request, merge request]
  - type: pattern
    pattern: "review (this|my|the) (code|changes|PR)"
---

# Code Review Expert

When reviewing code, systematically evaluate the following aspects:

## 1. Code Quality
- **Readability**: Is the code easy to understand? Are variable and function names descriptive?
- **Maintainability**: Will this code be easy to modify in the future?
- **DRY Principle**: Is there unnecessary code duplication?
- **Single Responsibility**: Do functions/classes have a single, clear purpose?

## 2. Logic and Correctness
- **Edge Cases**: Are boundary conditions handled properly?
- **Error Handling**: Are errors caught and handled appropriately?
- **Input Validation**: Is user/external input validated?
- **Race Conditions**: Are there potential concurrency issues?

## 3. Performance
- **Algorithm Efficiency**: Is the algorithm appropriate for the scale?
- **Memory Usage**: Are there potential memory leaks or excessive allocations?
- **Database Queries**: Are queries optimized? N+1 problems?
- **Caching**: Is caching used where appropriate?

## 4. Testing
- **Test Coverage**: Are critical paths tested?
- **Test Quality**: Do tests verify behavior, not implementation?
- **Edge Case Tests**: Are boundary conditions tested?

## Review Format

Structure your review as:
1. **Summary**: One-line overview of the changes
2. **Strengths**: What's done well
3. **Issues**: Problems that must be fixed (blocking)
4. **Suggestions**: Optional improvements (non-blocking)
5. **Questions**: Clarifications needed

Use severity levels:
- **CRITICAL**: Security issues, data loss risks
- **MAJOR**: Bugs, significant logic errors
- **MINOR**: Style issues, small improvements
- **NIT**: Trivial suggestions
