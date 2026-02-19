---
name: refactoring
description: Code refactoring patterns and techniques for improving code quality
tags: [refactor, clean-code, patterns]
tools: [read_file, list_files]
triggers:
  - type: keyword
    keywords: [refactor, refactoring, clean up, code smell, technical debt, simplify]
  - type: pattern
    pattern: "(refactor|clean up|simplify|improve) (this|the|my)"
---

# Refactoring Expert

Guide safe, incremental code improvement:

## Golden Rule
Refactoring changes structure without changing behavior. Always have tests before refactoring.

## Common Code Smells

### Function Level
- **Long function**: Break into smaller, focused functions
- **Too many parameters**: Group into a struct/object
- **Flag arguments**: Split into separate functions
- **Deep nesting**: Use early returns and guard clauses

### Class/Module Level
- **God class**: Split by responsibility
- **Feature envy**: Move method to the class it uses most
- **Data clumps**: Group related fields into a struct
- **Shotgun surgery**: Changes require touching many files

### Codebase Level
- **Duplicate code**: Extract into shared function
- **Dead code**: Remove unused functions and variables
- **Inconsistent naming**: Standardize conventions
- **Primitive obsession**: Use domain types instead of strings/ints

## Safe Refactoring Process
1. Ensure tests pass before starting
2. Make one small change at a time
3. Run tests after each change
4. Commit frequently (easy to revert)
5. Use automated refactoring tools when available

## Key Techniques
- **Extract function**: Pull code into a named function
- **Inline function**: Replace trivial wrapper with its body
- **Rename**: Make names express intent
- **Extract variable**: Name a complex expression
- **Move function**: Relocate to a better module
- **Replace conditional with polymorphism**: Use interfaces instead of switches
- **Introduce parameter object**: Group related parameters
