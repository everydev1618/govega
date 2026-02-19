---
name: api-design
description: REST API design patterns and best practices
tags: [api, rest, design, http]
tools: [read_file, write_file]
triggers:
  - type: keyword
    keywords: [API, endpoint, REST, route, handler, request, response, HTTP]
  - type: pattern
    pattern: "(design|create|build) (an? )?(API|endpoint|route)"
---

# API Design Expert

When designing or reviewing APIs, follow these principles:

## URL Structure
- Use nouns for resources: `/users`, `/orders`
- Use hierarchy for relationships: `/users/{id}/orders`
- Use query params for filtering: `/users?role=admin&active=true`
- Use kebab-case for multi-word resources: `/order-items`
- Keep URLs shallow (max 3 levels deep)

## HTTP Methods
| Method | Purpose | Idempotent | Response |
|--------|---------|------------|----------|
| GET | Read resource(s) | Yes | 200 + body |
| POST | Create resource | No | 201 + Location |
| PUT | Full update | Yes | 200 + body |
| PATCH | Partial update | No | 200 + body |
| DELETE | Remove resource | Yes | 204 no body |

## Response Format
```json
{
  "data": { ... },
  "meta": { "page": 1, "total": 100 }
}
```

For errors:
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Human-readable message",
    "details": [{ "field": "email", "message": "Invalid format" }]
  }
}
```

## Status Codes
- **200** OK (successful GET/PUT/PATCH)
- **201** Created (successful POST)
- **204** No Content (successful DELETE)
- **400** Bad Request (validation error)
- **401** Unauthorized (not authenticated)
- **403** Forbidden (not authorized)
- **404** Not Found
- **409** Conflict (duplicate, version mismatch)
- **422** Unprocessable Entity (semantic error)
- **429** Too Many Requests (rate limited)
- **500** Internal Server Error

## Pagination
Use cursor-based pagination for large datasets:
```
GET /users?cursor=abc123&limit=20
```

## Versioning
Prefer URL path versioning: `/v1/users`
