---
name: documentation
description: Technical documentation writing guidance
tags: [docs, documentation, writing]
triggers:
  - type: keyword
    keywords: [document, documentation, docs, readme, API docs, docstring]
  - type: pattern
    pattern: "(write|create|generate) (docs|documentation|readme)"
---

# Documentation Expert

When writing technical documentation, follow these principles:

## Documentation Types

### 1. README Files
Structure:
```markdown
# Project Name

Brief description (1-2 sentences)

## Installation

## Quick Start

## Usage

## API Reference (or link)

## Contributing

## License
```

### 2. API Documentation
For each endpoint/function:
- **Purpose**: What it does (one sentence)
- **Signature**: Function/endpoint definition
- **Parameters**: Name, type, description, required/optional
- **Returns**: Type and description
- **Errors**: Possible error conditions
- **Example**: Working code sample

### 3. Code Comments
- **Why, not what**: Explain reasoning, not mechanics
- **Document assumptions**: Edge cases, constraints
- **Keep updated**: Stale comments are worse than none

### 4. Architecture Docs
- **Overview diagram**: Visual system structure
- **Component descriptions**: Purpose of each part
- **Data flow**: How information moves through system
- **Decision records**: Why choices were made

## Best Practices

### Clarity
- Use simple, direct language
- Define acronyms on first use
- One idea per paragraph
- Active voice preferred

### Completeness
- Cover happy path AND error cases
- Include working examples
- Link to related documentation
- Version your docs with your code

### Maintenance
- Review docs during code review
- Automate doc generation where possible
- Test code examples in CI
- Date or version stamp documents

## Writing Checklist

Before publishing:
- [ ] Spell-checked and grammar-checked
- [ ] All code examples tested and working
- [ ] Links verified
- [ ] Reviewed by someone unfamiliar with the code
- [ ] Matches current code behavior
