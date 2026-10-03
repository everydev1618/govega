# Security Policy

## Supported versions

Vega is pre-1.0. Fixes land on the latest minor release; please upgrade before
reporting. The current release is listed at
https://github.com/everydev1618/vega-releases/releases/latest

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Use GitHub's private reporting instead:
[Report a vulnerability](https://github.com/everydev1618/govega/security/advisories/new)

Include the affected version, what an attacker can achieve, and a reproduction
if you have one. Expect an initial response within a week.

## Scope — what Vega does and does not protect

Vega runs LLM-directed agents that execute tools. Two boundaries are worth
stating plainly:

- **`exec` is not a sandbox.** The exec tool runs commands with the privileges
  of the Vega process. It is not a jail and is not intended as a security
  boundary. Only enable it for agents you trust, and run the server as an
  unprivileged user.
- **Prompt injection is a live risk.** Content an agent reads — web pages, MCP
  tool output, files, chat messages — can attempt to redirect it. Grant each
  agent the narrowest tool set that does its job.

Reports about either of these are still welcome when they show a concrete
escalation beyond the documented behavior — for example, a tool escaping limits
that are supposed to constrain it.

## Out of scope

- Costs incurred by an agent's own API usage
- Issues requiring an attacker to already control the host or the config file
- Vulnerabilities in the upstream model provider
