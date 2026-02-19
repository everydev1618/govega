---
name: testing
description: Test-driven development and test writing best practices
tags: [test, tdd, testing, quality]
tools: [read_file, write_file, list_files]
triggers:
  - type: keyword
    keywords: [test, testing, TDD, unit test, integration test, coverage, mock, assert]
  - type: pattern
    pattern: "(write|add|create) (a )?(test|tests|spec)"
---

# Testing Expert

Guide test writing and TDD practices:

## TDD Cycle
1. **Red**: Write a failing test that defines the expected behavior
2. **Green**: Write the minimum code to make the test pass
3. **Refactor**: Clean up the code while keeping tests green

## Test Structure (Arrange-Act-Assert)
```
func TestFeature(t *testing.T) {
    // Arrange: set up test data and dependencies
    // Act: call the function under test
    // Assert: verify the result matches expectations
}
```

## What to Test

### Unit Tests
- Public API behavior (inputs -> outputs)
- Edge cases and boundary conditions
- Error handling paths
- State transitions

### Integration Tests
- Component interactions
- Database operations
- External API calls (with mocks for CI)
- End-to-end workflows

## Test Naming
Use descriptive names that explain the scenario:
- `TestCreateUser_WithValidInput_ReturnsUser`
- `TestCreateUser_WithDuplicateEmail_ReturnsConflict`
- `TestCreateUser_WithMissingName_ReturnsValidationError`

## Best Practices
- Test behavior, not implementation
- One assertion per test (or one logical assertion)
- Keep tests independent (no shared mutable state)
- Use table-driven tests for multiple similar cases
- Mock external dependencies, not internal ones
- Aim for fast tests (< 1s per unit test)
- Use test fixtures for complex setup

## Test Doubles
- **Stub**: Returns canned responses
- **Mock**: Verifies interactions occurred
- **Fake**: Working implementation (e.g., in-memory DB)
- **Spy**: Records calls for later verification
