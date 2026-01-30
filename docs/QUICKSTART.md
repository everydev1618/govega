# Vega Quick Start

Get your first AI agent team running in 5 minutes.

## Installation

```bash
# macOS
brew install vega

# Linux
curl -fsSL https://get.vega.dev | sh

# Or download from GitHub releases
```

## Your First Agent

Create a file called `hello.vega.yaml`:

```yaml
agents:
  Assistant:
    model: claude-sonnet-4-20250514
    system: You are a helpful assistant.

workflows:
  ask:
    steps:
      - Assistant: "{{input}}"
    output: result
```

Run it:

```bash
export ANTHROPIC_API_KEY=your-key-here
vega run hello.vega.yaml --workflow ask --input "What is the capital of France?"
```

Output:
```
The capital of France is Paris.
```

That's it! You just ran your first Vega workflow.

---

## Building a Team

Let's create a team that writes and reviews code.

Create `team.vega.yaml`:

```yaml
name: Code Team

agents:
  Coder:
    model: claude-sonnet-4-20250514
    system: You write clean, simple code. Return only code, no explanations.

  Reviewer:
    model: claude-sonnet-4-20250514
    system: |
      You review code for bugs and improvements.
      If the code is good, say "APPROVED".
      Otherwise, list the issues.

workflows:
  write-code:
    steps:
      - Coder writes: "{{input}}"
    output: result

  code-review:
    inputs:
      task:
        description: What code to write
        required: true

    steps:
      - Coder writes the code:
          send: "Write: {{task}}"
          save: code

      - Reviewer checks it:
          send: "Review this code:\n{{code}}"
          save: review

    output: |
      ## Code
      {{code}}

      ## Review
      {{review}}
```

Run it:

```bash
vega run team.vega.yaml --workflow code-review --task "a Python function to check if a number is prime"
```

Output:
```
## Code
def is_prime(n):
    if n < 2:
        return False
    for i in range(2, int(n ** 0.5) + 1):
        if n % i == 0:
            return False
    return True

## Review
APPROVED. The code correctly implements a prime checker with an efficient
square root optimization. Clean and readable.
```

---

## Adding Iteration

What if the code isn't approved? Let's have the coder fix it:

```yaml
workflows:
  code-review-loop:
    inputs:
      task:
        required: true

    steps:
      - Coder: "Write: {{task}}"
        save: code

      - Reviewer: "Review:\n{{code}}"
        save: review

      - if: "'APPROVED' not in review"
        then:
          - Coder fixes issues:
              send: |
                Fix these issues:
                {{review}}

                Original code:
                {{code}}
              save: code

          - Reviewer: "Review again:\n{{code}}"
            save: review

    output: |
      ## Final Code
      {{code}}

      ## Final Review
      {{review}}
```

---

## Parallel Execution

Have multiple agents work at the same time:

```yaml
workflows:
  research:
    inputs:
      topic:
        required: true

    steps:
      - parallel:
          - Optimist: "What's great about {{topic}}?"
            save: pros
          - Pessimist: "What are the risks of {{topic}}?"
            save: cons
          - Pragmatist: "What's the realistic outlook for {{topic}}?"
            save: reality

      - Synthesizer:
          send: |
            Combine these perspectives on {{topic}}:

            Pros: {{pros}}
            Cons: {{cons}}
            Reality: {{reality}}
          save: synthesis

    output: "{{synthesis}}"
```

---

## Using Tools

Give agents the ability to read and write files:

```yaml
agents:
  FileBot:
    model: claude-sonnet-4-20250514
    system: You help users manage files.
    tools:
      - read_file
      - write_file
      - list_files

workflows:
  organize:
    steps:
      - FileBot: |
          List all files in ./documents
          Then read each one and create a summary file
          called ./documents/SUMMARY.md
    output: result
```

---

## Workflows Calling Workflows

Build complex systems from simple parts:

```yaml
workflows:
  # Simple building block
  review-code:
    inputs: [code]
    steps:
      - Reviewer: "Review:\n{{code}}"
    output: result

  # Larger workflow uses the building block
  full-pipeline:
    inputs: [task]
    steps:
      - Coder: "Write: {{task}}"
        save: code

      - workflow: review-code
        with:
          code: "{{code}}"
        save: review

    output: "{{code}}\n\n{{review}}"
```

---

## Interactive Mode

Explore your team interactively:

```bash
$ vega repl team.vega.yaml

vega> Coder <- "Write a hello world function"
def hello():
    print("Hello, world!")

vega> save my_code

vega> Reviewer <- "Review: {{my_code}}"
APPROVED. Simple and correct.

vega> run code-review --task "fibonacci sequence"
[Coder] Writing code...
[Reviewer] Reviewing...
✓ Complete

vega> exit
```

---

## What's Next?

- Read the [full DSL reference](DSL.md)
- See [example projects](../examples/)
- Learn about [supervision and fault tolerance](SUPERVISION.md)
- Set up [budgets and cost control](SPEC.md#budgets--cost-control)

---

## Common Patterns

### Content Pipeline

```yaml
workflows:
  write-article:
    steps:
      - Writer: "Write about {{topic}}"
        save: draft

      - Editor: "Edit for clarity:\n{{draft}}"
        save: edited

      - FactChecker: "Verify claims:\n{{edited}}"
        save: checked

    output: "{{checked}}"
```

### Customer Support

```yaml
workflows:
  support:
    steps:
      - Classifier: |
          Classify this support ticket:
          {{ticket}}

          Categories: billing, technical, general
        save: category

      - if: "'billing' in category"
        then:
          - BillingAgent: "Handle billing issue:\n{{ticket}}"
            save: response
        else:
          - if: "'technical' in category"
            then:
              - TechAgent: "Handle technical issue:\n{{ticket}}"
                save: response
            else:
              - GeneralAgent: "Handle general inquiry:\n{{ticket}}"
                save: response

    output: "{{response}}"
```

### Research Assistant

```yaml
workflows:
  research:
    steps:
      - Researcher:
          send: "Research: {{question}}"
          tools: [web_search]
          save: findings

      - Analyst: |
          Analyze these findings and provide insights:
          {{findings}}
        save: analysis

      - Writer: |
          Write a clear summary:
          {{analysis}}
        save: summary

    output: "{{summary}}"
```

---

## Tips

1. **Start simple** — Get one agent working before building teams
2. **Use descriptive names** — `Coder writes the function:` is clearer than `step1:`
3. **Save intermediate results** — Use `save:` to debug and reuse outputs
4. **Test in REPL** — Use `vega repl` to experiment before writing workflows
5. **Check your budget** — Set `budget:` to avoid surprise costs
