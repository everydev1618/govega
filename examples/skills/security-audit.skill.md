---
name: security-audit
description: Security vulnerability assessment and secure coding guidance
tags: [security, audit, vulnerability]
tools: [read_file]
triggers:
  - type: keyword
    keywords: [security, vulnerability, CVE, exploit, injection, XSS, CSRF, auth]
  - type: pattern
    pattern: "(security|secure|vulnerability|exploit)"
---

# Security Audit Expert

When analyzing code for security issues, check for these vulnerability categories:

## OWASP Top 10

### 1. Injection (SQL, Command, LDAP)
- [ ] Parameterized queries used for all database operations
- [ ] User input never concatenated into queries
- [ ] Command execution uses safe APIs, not shell interpolation

### 2. Broken Authentication
- [ ] Strong password requirements enforced
- [ ] Multi-factor authentication available
- [ ] Session tokens are cryptographically secure
- [ ] Session timeout implemented

### 3. Sensitive Data Exposure
- [ ] Sensitive data encrypted at rest
- [ ] TLS used for data in transit
- [ ] No sensitive data in logs or URLs
- [ ] Proper key management

### 4. XML External Entities (XXE)
- [ ] XML parsing disables external entities
- [ ] DTD processing disabled when not needed

### 5. Broken Access Control
- [ ] Authorization checked on every request
- [ ] Principle of least privilege applied
- [ ] Direct object references validated

### 6. Security Misconfiguration
- [ ] Default credentials changed
- [ ] Unnecessary features disabled
- [ ] Error messages don't leak information

### 7. Cross-Site Scripting (XSS)
- [ ] Output encoding applied
- [ ] Content Security Policy headers set
- [ ] User input sanitized before rendering

### 8. Insecure Deserialization
- [ ] Serialized data validated before processing
- [ ] Type constraints enforced

### 9. Using Components with Known Vulnerabilities
- [ ] Dependencies up to date
- [ ] No known CVEs in dependencies

### 10. Insufficient Logging & Monitoring
- [ ] Security events logged
- [ ] Logs protected from tampering
- [ ] Alerting configured for suspicious activity

## Report Format

For each finding:
```
**[SEVERITY] Finding Title**
- Location: file:line
- Description: What the vulnerability is
- Impact: What could happen if exploited
- Remediation: How to fix it
- References: CVE numbers, OWASP links
```

Severity Levels:
- **CRITICAL**: Immediate exploitation risk, data breach likely
- **HIGH**: Significant risk, should fix before deployment
- **MEDIUM**: Notable risk, fix in near term
- **LOW**: Minor risk, fix when convenient
- **INFO**: Best practice suggestion, no immediate risk
