# Vega DSL Specification

**Version:** 0.1.0
**Status:** Draft

Vega DSL is a YAML-based language for defining AI agent systems. It's designed to be readable and writable by non-programmers while providing enough power for complex workflows.

## Philosophy

1. **Readable first** — Code is read more than written. Optimize for clarity.
2. **Progressive complexity** — Simple things are simple. Complex things are possible.
3. **Fail helpfully** — Error messages guide users to solutions.
4. **No programming required** — But programmers can extend it.

---

## File Structure

Vega projects use `.vega.yaml` files:

```
my-project/
├── agents.vega.yaml      # Agent definitions
├── workflows.vega.yaml   # Workflow definitions
├── tools/                # Custom tool definitions
│   ├── search.yaml
│   └── email.yaml
└── knowledge/            # Knowledge files for agents
    └── style-guide.md
```

Or everything in one file:

```
my-project/
└── team.vega.yaml        # Agents + workflows + tools
```

---

## Basic Structure

```yaml
# team.vega.yaml

name: My Agent Team
description: A team that writes and reviews code

# Agent definitions
agents:
  Coder:
    # ...
  Reviewer:
    # ...

# Workflow definitions
workflows:
  code-review:
    # ...

# Tool definitions (optional - can also load from files)
tools:
  web_search:
    # ...

# Global settings (optional)
settings:
  default_model: claude-sonnet-4-20250514
  sandbox: ./workspace
  budget: $10.00
```

---

## Agents

### Basic Agent

```yaml
agents:
  Coder:
    model: claude-sonnet-4-20250514
    system: You write clean, efficient code.
```

### Full Agent Definition

```yaml
agents:
  Coder:
    # Display name (optional, defaults to key)
    name: Senior Developer

    # Model selection
    model: claude-sonnet-4-20250514

    # System prompt (required)
    system: |
      You are a senior developer who writes clean, tested code.

      Guidelines:
      - Write readable code over clever code
      - Include error handling
      - Add comments for complex logic

    # Temperature (optional, default: 0.7)
    temperature: 0.3

    # Cost limit per task (optional)
    budget: $0.50

    # Tools this agent can use (optional)
    tools:
      - read_file
      - write_file
      - list_files
      - run_command

    # Knowledge files to include in context (optional)
    knowledge:
      - knowledge/coding-standards.md
      - knowledge/api-docs.md

    # Supervision settings (optional)
    supervision:
      strategy: restart      # restart, stop, escalate
      max_restarts: 3
      window: 10m

    # Retry settings for API failures (optional)
    retry:
      max_attempts: 3
      backoff: exponential   # linear, exponential, constant
```

### Agent Inheritance

Agents can extend other agents:

```yaml
agents:
  BaseAgent:
    model: claude-sonnet-4-20250514
    temperature: 0.3
    supervision:
      strategy: restart
      max_restarts: 3

  Coder:
    extends: BaseAgent
    system: You write code.
    tools: [read_file, write_file]

  Reviewer:
    extends: BaseAgent
    system: You review code.
    tools: [read_file]
```

---

## Tools

### Built-in Tools

These are available by default:

| Tool | Description |
|------|-------------|
| `read_file` | Read a file's contents |
| `write_file` | Write content to a file |
| `append_file` | Append content to a file |
| `list_files` | List files in a directory |
| `run_command` | Execute a shell command |
| `web_search` | Search the web |
| `http_get` | Make HTTP GET request |
| `http_post` | Make HTTP POST request |

### Custom Tools (YAML)

```yaml
tools:
  send_slack:
    description: Send a message to Slack
    params:
      channel:
        type: string
        description: Channel name
        required: true
      message:
        type: string
        description: Message to send
        required: true
    implementation:
      type: http
      method: POST
      url: https://hooks.slack.com/services/${SLACK_WEBHOOK}
      body:
        channel: "{{channel}}"
        text: "{{message}}"

  search_docs:
    description: Search internal documentation
    params:
      query:
        type: string
        required: true
    implementation:
      type: http
      method: GET
      url: https://docs.internal.com/api/search
      query:
        q: "{{query}}"
```

### Tool Files

Tools can be defined in separate files:

```yaml
# tools/email.yaml
name: send_email
description: Send an email via SendGrid
params:
  to:
    type: string
    required: true
  subject:
    type: string
    required: true
  body:
    type: string
    required: true
implementation:
  type: http
  method: POST
  url: https://api.sendgrid.com/v3/mail/send
  headers:
    Authorization: Bearer ${SENDGRID_API_KEY}
  body:
    personalizations:
      - to: [{ email: "{{to}}" }]
    from: { email: "noreply@example.com" }
    subject: "{{subject}}"
    content:
      - type: text/plain
        value: "{{body}}"
```

Load in main file:

```yaml
tools:
  include:
    - tools/email.yaml
    - tools/slack.yaml
```

---

## Workflows

Workflows define how agents collaborate.

### Simple Workflow

```yaml
workflows:
  ask-coder:
    steps:
      - Coder responds: "{{input}}"
    output: result
```

Run with:
```bash
vega run --workflow ask-coder --input "Write hello world"
```

### Multi-Step Workflow

```yaml
workflows:
  code-review:
    description: Write and review code

    inputs:
      task:
        type: string
        description: What to build

    steps:
      - Coder writes the code:
          send: "Write: {{task}}"
          save: code

      - Reviewer reviews it:
          send: |
            Review this code for bugs and style:

            {{code}}
          save: review

    output: |
      ## Code
      {{code}}

      ## Review
      {{review}}
```

### Step Syntax

Steps have a natural language format:

```yaml
steps:
  # Format: "<Agent> <action description>:"
  - Coder writes the code:
      send: "Write {{task}}"
      save: code

  # Short form (agent name only)
  - Reviewer:
      send: "Review: {{code}}"
      save: review

  # Minimal form
  - Coder: "Just do {{task}}"
```

### Step Options

```yaml
steps:
  - Coder writes code:
      # Message to send (required)
      send: "Write {{task}}"

      # Save result to variable (optional)
      save: code

      # Timeout (optional)
      timeout: 5m

      # Budget for this step (optional)
      budget: $0.25

      # Retry on failure (optional)
      retry: 3

      # Condition (optional)
      if: "needs_code"

      # Continue even if this fails (optional)
      continue_on_error: true
```

---

## Expressions

Expressions use `{{...}}` syntax and support:

### Variables

```yaml
steps:
  - Coder: "Write {{task}}"           # Input variable
    save: code

  - Reviewer: "Review {{code}}"        # Previous step output
    save: review
```

### Built-in Variables

| Variable | Description |
|----------|-------------|
| `{{input}}` | Default input (for single-input workflows) |
| `{{step_name}}` | Output of a named step |
| `{{steps.name.output}}` | Explicit step reference |
| `{{env.VAR_NAME}}` | Environment variable |
| `{{date}}` | Current date (YYYY-MM-DD) |
| `{{time}}` | Current time (HH:MM:SS) |
| `{{random}}` | Random UUID |

### String Operations

```yaml
send: "{{task | upper}}"              # UPPERCASE
send: "{{task | lower}}"              # lowercase
send: "{{task | trim}}"               # Remove whitespace
send: "{{task | truncate:100}}"       # Limit length
send: "{{code | lines}}"              # Line count
send: "{{code | words}}"              # Word count
```

### Conditionals in Expressions

```yaml
send: "{{code if needs_code else 'No code needed'}}"
output: "{{final if approved else draft}}"
```

### Default Values

```yaml
send: "Write about {{topic | default:'AI agents'}}"
```

---

## Control Flow

### Conditionals

```yaml
steps:
  - Coder writes:
      send: "Write {{task}}"
      save: code

  - Reviewer checks:
      send: "Review {{code}}. Say APPROVED or list issues."
      save: review

  # Conditional step
  - if: "'APPROVED' not in review"
    then:
      - Coder fixes:
          send: "Fix these issues:\n{{review}}"
          save: code
```

### If/Else

```yaml
steps:
  - Classifier:
      send: "Classify sentiment: {{text}}"
      save: sentiment

  - if: "'POSITIVE' in sentiment"
    then:
      - Responder: "Write a thank you message"
        save: response
    else:
      - Responder: "Write an apology and offer help"
        save: response
```

### Loops

```yaml
steps:
  # Iterate over a list
  - for item in items:
      - Processor:
          send: "Process: {{item}}"
          save: "result_{{loop.index}}"

  # Repeat until condition
  - repeat:
      steps:
        - Coder: "Improve the code:\n{{code}}"
          save: code
        - Reviewer: "Review:\n{{code}}"
          save: review
      until: "'APPROVED' in review"
      max: 5
```

### Loop Variables

Inside loops, these variables are available:

| Variable | Description |
|----------|-------------|
| `{{loop.index}}` | Current iteration (0-based) |
| `{{loop.count}}` | Current iteration (1-based) |
| `{{loop.first}}` | True if first iteration |
| `{{loop.last}}` | True if last iteration |
| `{{item}}` | Current item (for-each loops) |

---

## Parallel Execution

### Parallel Steps

```yaml
steps:
  - parallel:
      - Researcher1: "Research {{topic}} from perspective A"
        save: research_a
      - Researcher2: "Research {{topic}} from perspective B"
        save: research_b
      - Researcher3: "Research {{topic}} from perspective C"
        save: research_c

  - Synthesizer:
      send: |
        Combine these perspectives:
        A: {{research_a}}
        B: {{research_b}}
        C: {{research_c}}
      save: synthesis
```

### Parallel with Same Agent

```yaml
steps:
  - parallel:
      agent: Analyzer
      inputs:
        - "Analyze document 1"
        - "Analyze document 2"
        - "Analyze document 3"
      save: analyses

  - Summarizer: "Summarize: {{analyses | join:'\n\n'}}"
```

---

## Error Handling

### Continue on Error

```yaml
steps:
  - Risky operation:
      send: "Try something risky"
      save: result
      continue_on_error: true

  - if: "error in result"
    then:
      - Fallback: "Do the safe alternative"
```

### Try/Catch

```yaml
steps:
  - try:
      - ExternalAPI: "Call the flaky API"
        save: data
    catch:
      - Fallback: "Use cached data instead"
        save: data
```

### Timeout Handling

```yaml
steps:
  - SlowAgent:
      send: "Do something slow"
      timeout: 30s
      on_timeout:
        - FastAgent: "Do quick alternative"
```

---

## Workflows Calling Workflows

### Sub-workflows

```yaml
workflows:
  review-code:
    inputs: [code]
    steps:
      - Reviewer: "Review:\n{{code}}"
        save: review
    output: review

  full-pipeline:
    inputs: [task]
    steps:
      - Coder: "Write: {{task}}"
        save: code

      # Call another workflow
      - workflow: review-code
        with:
          code: "{{code}}"
        save: review

    output: "{{code}}\n\n{{review}}"
```

### Recursive Workflows

```yaml
workflows:
  refine-until-approved:
    inputs: [code, max_attempts]

    steps:
      - Reviewer: "Review:\n{{code}}"
        save: review

      - if: "'APPROVED' in review or max_attempts <= 0"
        then:
          - return: "{{code}}"
        else:
          - Coder: "Fix:\n{{review}}"
            save: improved
          - workflow: refine-until-approved
            with:
              code: "{{improved}}"
              max_attempts: "{{max_attempts - 1}}"
            save: final
          - return: "{{final}}"
```

---

## Inputs and Outputs

### Input Definitions

```yaml
workflows:
  write-article:
    inputs:
      topic:
        type: string
        description: Article topic
        required: true

      tone:
        type: string
        description: Writing tone
        default: professional
        enum: [professional, casual, technical]

      word_count:
        type: number
        description: Target word count
        default: 1000
        min: 100
        max: 5000
```

### Output Definitions

```yaml
workflows:
  analyze-code:
    # ... steps ...

    output:
      summary: "{{analysis.summary}}"
      issues: "{{analysis.issues}}"
      score: "{{analysis.score}}"
```

### Structured Output

```yaml
workflows:
  extract-info:
    steps:
      - Extractor:
          send: |
            Extract from this text:
            {{text}}

            Return JSON with: name, email, phone
          save: extracted
          format: json    # Parse output as JSON

    output:
      name: "{{extracted.name}}"
      email: "{{extracted.email}}"
      phone: "{{extracted.phone}}"
```

---

## Memory and State

### Workflow Memory

Variables persist within a workflow run:

```yaml
steps:
  - set:
      attempt_count: 0
      max_attempts: 3

  - repeat:
      steps:
        - set:
            attempt_count: "{{attempt_count + 1}}"
        - Coder: "Attempt {{attempt_count}}: {{task}}"
      until: "success or attempt_count >= max_attempts"
```

### Persistent Memory

Store data across workflow runs:

```yaml
steps:
  # Save to persistent memory
  - remember:
      key: "user_{{user_id}}_preferences"
      value: "{{preferences}}"

  # Recall from persistent memory
  - recall:
      key: "user_{{user_id}}_preferences"
      save: preferences
      default: "{}"
```

---

## Settings

### Global Settings

```yaml
settings:
  # Default model for all agents
  default_model: claude-sonnet-4-20250514

  # Default temperature
  default_temperature: 0.7

  # File sandbox directory
  sandbox: ./workspace

  # Global budget limit
  budget: $50.00

  # Default supervision
  supervision:
    strategy: restart
    max_restarts: 3

  # Rate limiting
  rate_limit:
    requests_per_minute: 60

  # Logging
  logging:
    level: info        # debug, info, warn, error
    file: ./vega.log

  # Tracing
  tracing:
    enabled: true
    exporter: otlp
    endpoint: localhost:4317
```

### Environment Variables

Reference environment variables anywhere:

```yaml
agents:
  Coder:
    model: ${MODEL_NAME:-claude-sonnet-4-20250514}
    system: ${CODER_SYSTEM_PROMPT}

settings:
  api_key: ${ANTHROPIC_API_KEY}
```

---

## CLI Interface

### Running Workflows

```bash
# Run a workflow
vega run team.vega.yaml --workflow code-review --task "Build a REST API"

# With multiple inputs
vega run team.vega.yaml --workflow write-article \
  --topic "AI Agents" \
  --tone casual \
  --word_count 500

# From stdin
echo "Fix this bug" | vega run team.vega.yaml --workflow code-review

# Output to file
vega run team.vega.yaml --workflow code-review --task "..." --output result.md
```

### Validation

```bash
# Validate configuration
vega validate team.vega.yaml

# Verbose validation
vega validate team.vega.yaml --verbose

# Output:
# ✓ Syntax valid
# ✓ 3 agents defined: Coder, Reviewer, Editor
# ✓ 2 workflows defined: code-review, write-article
# ✓ All tool references resolved
# ✓ All agent references in workflows resolved
# ⚠ Warning: Agent 'Editor' is defined but not used in any workflow
```

### Interactive Mode (REPL)

```bash
$ vega repl team.vega.yaml

vega> Coder <- "Write hello world in Python"
def hello():
    print("Hello, world!")

hello()

vega> save code

vega> Reviewer <- "Review this:\n{{code}}"
The code looks good! A few suggestions:
1. Add a docstring
2. Consider adding a main guard

vega> run code-review --task "binary search"
[Coder] Writing code...
[Reviewer] Reviewing...
✓ Complete

vega> help
Commands:
  <Agent> <- "message"    Send message to agent
  run <workflow>          Run a workflow
  save <name>             Save last result to variable
  show <name>             Show variable value
  list agents             List available agents
  list workflows          List available workflows
  help                    Show this help
  exit                    Exit REPL

vega> exit
```

### Other Commands

```bash
# List agents and workflows
vega list team.vega.yaml

# Show agent details
vega show agent Coder --file team.vega.yaml

# Show workflow details
vega show workflow code-review --file team.vega.yaml

# Dry run (show what would happen)
vega run team.vega.yaml --workflow code-review --task "..." --dry-run

# Debug mode (verbose output)
vega run team.vega.yaml --workflow code-review --task "..." --debug

# Watch mode (re-run on file changes)
vega watch team.vega.yaml --workflow code-review --task "..."
```

---

## Complete Example

```yaml
# content-team.vega.yaml

name: Content Team
description: A team that creates high-quality articles

settings:
  default_model: claude-sonnet-4-20250514
  sandbox: ./workspace
  budget: $5.00

agents:
  Marcus:
    name: Marcus the Writer
    system: |
      You are Marcus, a skilled ghostwriter.
      You write compelling articles that combine personal stories
      with actionable insights.

      Style guidelines:
      - Use present tense for stories
      - Include specific details (dates, numbers, names)
      - End with a clear call to action
    temperature: 0.7
    tools: [read_file, write_file, web_search]
    budget: $1.00

  Vera:
    name: Vera the Fact-Checker
    system: |
      You are Vera, a meticulous fact-checker.
      Verify all claims and find authoritative sources.
      Flag anything that can't be verified.
    temperature: 0.2
    tools: [web_search, read_file]

  Claire:
    name: Claire the Editor
    system: |
      You are Claire, an experienced editor.
      Polish writing for clarity, flow, and voice consistency.
      Preserve the author's unique perspective.
    temperature: 0.4
    tools: [read_file, write_file]

workflows:
  write-article:
    description: Create a fact-checked, polished article

    inputs:
      topic:
        type: string
        description: Article topic
        required: true
      story:
        type: string
        description: Personal story to anchor the article
        required: true
      slug:
        type: string
        description: URL-friendly identifier
        required: true

    steps:
      - Marcus writes the first draft:
          send: |
            Write an article about: {{topic}}

            Personal story to include:
            {{story}}

            Save to: writings/{{slug}}/draft.md
          save: draft

      - Vera fact-checks the draft:
          send: |
            Fact-check this article:
            {{draft}}

            For each claim:
            1. Verify if true/false/unverifiable
            2. Provide source if available
            3. Suggest corrections if needed
          save: fact_check

      - Claire edits for voice and clarity:
          send: |
            Edit this article for voice and clarity:
            {{draft}}

            Fact-check notes to consider:
            {{fact_check}}
          save: editorial

      - Marcus writes final version:
          send: |
            Revise the article based on feedback:

            Original draft:
            {{draft}}

            Fact-check report:
            {{fact_check}}

            Editorial feedback:
            {{editorial}}

            Save final version to: writings/{{slug}}/article.md
          save: final

    output: |
      Article complete: writings/{{slug}}/article.md

      {{final}}

  quick-post:
    description: Write a quick social media post

    inputs:
      topic:
        type: string
        required: true
      platform:
        type: string
        default: linkedin
        enum: [linkedin, twitter, facebook]

    steps:
      - Marcus: |
          Write a {{platform}} post about: {{topic}}

          Keep it engaging and concise.
          Include a call to action.
        save: post

    output: "{{post}}"
```

Run it:

```bash
# Full article workflow
vega run content-team.vega.yaml --workflow write-article \
  --topic "Why CTOs should embrace technical debt" \
  --story "Last quarter, I inherited a codebase with 10 years of debt..." \
  --slug "cto-technical-debt"

# Quick post
vega run content-team.vega.yaml --workflow quick-post \
  --topic "AI agents are changing how we build software" \
  --platform linkedin
```

---

## Grammar Reference

### EBNF (Simplified)

```ebnf
document     = name? description? agents? workflows? tools? settings?

agents       = "agents:" agent+
agent        = identifier ":" agent_body
agent_body   = (extends | model | system | temperature | budget |
                tools | knowledge | supervision | retry)+

workflows    = "workflows:" workflow+
workflow     = identifier ":" workflow_body
workflow_body = description? inputs? steps output?

steps        = "steps:" step+
step         = agent_step | control_step | parallel_step | workflow_step

agent_step   = agent_name action? ":" step_body
step_body    = send save? timeout? budget? retry? if? continue_on_error?

control_step = if_step | for_step | repeat_step | try_step
if_step      = "if:" expression "then:" steps ("else:" steps)?
for_step     = "for" identifier "in" expression ":" steps
repeat_step  = "repeat:" steps "until:" expression "max:"? number?

expression   = "{{" expr_content "}}"
expr_content = variable | variable "|" filter | conditional
```

---

## Error Messages

Vega provides helpful error messages:

```
Error: Unknown agent 'Cder' in workflow 'code-review'

  → Did you mean 'Coder'?

  At: team.vega.yaml:45

    44 |     steps:
    45 |       - Cder writes code:    ← here
    46 |           send: "Write {{task}}"
```

```
Error: Missing required input 'topic' for workflow 'write-article'

  The workflow expects these inputs:
    - topic (string, required)
    - tone (string, optional, default: "professional")

  Usage:
    vega run team.vega.yaml --workflow write-article --topic "Your topic"
```

```
Error: Undefined variable 'reveiw' in expression

  → Did you mean 'review'?

  At: team.vega.yaml:52

    51 |       - if: "'APPROVED' not in reveiw"
                                        ^^^^^^

  Available variables at this point:
    - task (input)
    - code (from step 1)
    - review (from step 2)
```

---

## Appendix: Expression Functions

### String Functions

| Function | Description | Example |
|----------|-------------|---------|
| `upper` | Uppercase | `{{name\|upper}}` → "JOHN" |
| `lower` | Lowercase | `{{name\|lower}}` → "john" |
| `trim` | Remove whitespace | `{{text\|trim}}` |
| `truncate:n` | Limit to n chars | `{{text\|truncate:100}}` |
| `replace:a:b` | Replace a with b | `{{text\|replace:old:new}}` |
| `split:sep` | Split into list | `{{text\|split:,}}` |
| `join:sep` | Join list | `{{items\|join:\n}}` |
| `lines` | Count lines | `{{text\|lines}}` |
| `words` | Count words | `{{text\|words}}` |
| `chars` | Count characters | `{{text\|chars}}` |

### List Functions

| Function | Description | Example |
|----------|-------------|---------|
| `first` | First item | `{{items\|first}}` |
| `last` | Last item | `{{items\|last}}` |
| `length` | List length | `{{items\|length}}` |
| `reverse` | Reverse list | `{{items\|reverse}}` |
| `sort` | Sort list | `{{items\|sort}}` |
| `unique` | Remove duplicates | `{{items\|unique}}` |
| `slice:start:end` | Slice list | `{{items\|slice:0:5}}` |

### Conditional Functions

| Function | Description | Example |
|----------|-------------|---------|
| `default:val` | Default if empty | `{{name\|default:Anonymous}}` |
| `if:cond:then:else` | Ternary | `{{x\|if:>0:positive:negative}}` |

### Type Functions

| Function | Description | Example |
|----------|-------------|---------|
| `json` | Parse as JSON | `{{response\|json}}` |
| `yaml` | Parse as YAML | `{{response\|yaml}}` |
| `int` | Convert to integer | `{{count\|int}}` |
| `float` | Convert to float | `{{price\|float}}` |
| `string` | Convert to string | `{{number\|string}}` |
