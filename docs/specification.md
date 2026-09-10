# AI Coding Activity Observability Platform

## 1. Project Overview

Build a lightweight, technology-agnostic platform that sits between developers and AI coding agents/IDEs and collects structured information about AI-assisted software development.

The platform must allow a developer or organization to:

1. Register a project.
2. Generate an API key for that project.
3. Integrate the platform with any coding agent, IDE, CLI, plugin, or custom development tool.
4. Track which developer/user initiated an AI interaction.
5. Track which IDE was being used.
6. Track which coding agent was being used.
7. Track which AI model/provider was used.
8. Track prompts and AI requests.
9. Track token usage.
10. Track estimated/actual cost where available.
11. Track agent/tool activity.
12. Track files read, created, modified, renamed, or deleted.
13. Track code changes/diffs.
14. Attribute changes to AI-assisted sessions/prompts whenever technically possible.
15. Track Git activity.
16. Provide project-level, user-level, session-level, prompt-level, agent-level, model-level, and time-based analytics.
17. Work across programming languages and technology stacks.
18. Remain lightweight enough that tracking must not noticeably affect developer workflows.

The platform is NOT a Java-specific product.

A project being monitored may contain:

* Java
* Python
* JavaScript
* TypeScript
* Go
* Rust
* C
* C++
* C#
* Kotlin
* Swift
* PHP
* Ruby
* Dart
* or any other programming language.

The platform must treat programming language as metadata rather than a core dependency.

---

# 2. Core Product Principle

The system must be:

* IDE agnostic
* Coding-agent agnostic
* Programming-language agnostic
* Model/provider agnostic
* Operating-system agnostic
* Integration-method agnostic
* Cloud-provider agnostic
* Database implementation agnostic where practical

Do NOT build the core system around one particular IDE, coding agent, programming language, LLM provider, or cloud provider.

For example, do not create business logic such as:

```text
if language == Java
```

or:

```text
if agent == OpenCode
```

Agent-specific behavior must exist only inside an adapter/integration layer.

---

# 3. High-Level Architecture

The intended architecture is:

Developer / IDE / Coding Agent
|
v
Integration Layer
|
v
Event Collection API
|
v
Event Processing
|
v
Storage / Analytics
|
v
Dashboard

````

The integration layer may be implemented using:

- REST API
- HTTP API
- SDK
- CLI
- IDE plugin
- Coding-agent plugin
- Local collector
- Webhook
- Custom adapter

No single integration mechanism is mandatory.

---

# 4. Technology-Neutral Integration Contract

The most important interface of the platform is the event contract.

Any technology should be able to send an event using HTTP.

For example:

```http
POST /api/v1/events
Authorization: Bearer <API_KEY>
Content-Type: application/json
````

Example:

```json
{
  "event_type": "llm_request",
  "project_id": "project_123",
  "user_id": "user_123",
  "session_id": "session_123",
  "trace_id": "trace_123",
  "timestamp": "2026-09-10T18:30:00Z",
  "agent": {
    "name": "example-agent",
    "version": "1.0.0"
  },
  "ide": {
    "name": "vscode",
    "version": "1.100"
  },
  "language": "typescript",
  "model": {
    "provider": "example-provider",
    "name": "example-model"
  },
  "usage": {
    "input_tokens": 1000,
    "output_tokens": 500,
    "cached_tokens": 200,
    "reasoning_tokens": 100
  }
}
```

The server must not require a specific SDK.

A developer should be able to implement the integration in:

```text
Java
Python
JavaScript
TypeScript
Go
Rust
C#
C++
PHP
Ruby
etc.
```

using standard HTTP.

---

# 5. Identity Model

The platform must distinguish between:

* Organization
* User
* API key
* Project
* Repository
* IDE
* Coding agent
* AI model
* Session
* Trace
* Prompt
* Tool call
* File change
* Git event

Relationship:

```text
Organization
    |
    +---- Users
    |
    +---- Projects
              |
              +---- Repositories
              |
              +---- Sessions
                        |
                        +---- Traces
                                  |
                                  +---- Prompts
                                  |
                                  +---- LLM Requests
                                  |
                                  +---- Tool Calls
                                  |
                                  +---- File Changes
                                  |
                                  +---- Git Events
```

---

# 6. User Identification

Every activity should be attributable to a user whenever the integration provides a user identity.

Required user fields:

```text
user_id
display_name
email (optional)
organization_id
created_at
```

Do not require an email address to identify a user.

The system should support:

```text
internal user ID
external user ID
Git identity
organization identity
```

Example:

```json
{
  "user_id": "usr_123",
  "external_user_id": "github_987"
}
```

---

# 7. Project Identification

Each monitored repository/project must have a stable project ID.

Example:

```text
project_id = prj_123
```

Project metadata:

```text
project_id
name
description
repository_url
default_branch
created_at
```

A project may contain multiple programming languages.

Example:

```text
Project: ecommerce-platform

Java
TypeScript
Python
SQL
Dockerfile
Terraform
```

Do not assume one project equals one programming language.

---

# 8. Repository Identification

Where Git is available, capture:

```text
repository URL
repository name
branch
commit SHA
remote
```

A repository may be:

* GitHub
* GitLab
* Bitbucket
* Azure DevOps
* self-hosted Git
* another Git-compatible system

Do not make GitHub mandatory.

---

# 9. IDE Information

Capture IDE information when available.

Example:

```json
{
  "ide": {
    "name": "IntelliJ IDEA",
    "version": "2026.x",
    "type": "desktop"
  }
}
```

Examples:

```text
VS Code
IntelliJ IDEA
Eclipse
Visual Studio
Cursor
Windsurf
Neovim
Vim
Emacs
Android Studio
Xcode
terminal
custom IDE
```

The IDE field must be extensible.

Do not create hard-coded business logic for a specific IDE.

---

# 10. Coding Agent Information

Capture:

```text
agent_name
agent_version
agent_type
```

Examples:

```text
OpenCode
Claude Code
Cursor Agent
custom internal agent
GitHub-based agent
company-specific coding agent
```

The platform must support unknown/custom agents.

Example:

```json
{
  "agent": {
    "name": "my-company-agent",
    "version": "2.4.1",
    "type": "custom"
  }
}
```

---

# 11. AI Model Information

Capture:

```text
provider
model
model_version (optional)
```

Examples:

```text
OpenAI
Anthropic
Google
Azure
AWS
local model
self-hosted model
custom provider
```

Do not assume that every request has a model name.

If the integration does not know the model, allow:

```text
model = unknown
```

Do not reject the event.

---

# 12. Prompt Tracking

Every AI prompt should have a unique prompt ID.

Example:

```text
prompt_id = prompt_123
```

Capture:

```text
prompt_id
user_id
project_id
session_id
trace_id
timestamp
agent
IDE
model
prompt content
prompt size
```

Prompt content must be optional.

The platform must support a privacy mode where only metadata is stored.

Possible modes:

```text
FULL
METADATA_ONLY
REDACTED
```

Do not make full prompt storage mandatory.

---

# 13. Token Tracking

For every LLM request, capture usage when available:

```text
input_tokens
output_tokens
cached_tokens
reasoning_tokens
total_tokens
```

If the provider supplies additional token categories, allow extensible metadata.

Do not assume every provider exposes the same token fields.

If only total tokens are available, store total tokens.

Do not reject incomplete usage information.

---

# 14. Cost Tracking

The platform should support:

```text
actual_cost
estimated_cost
currency
pricing_version
```

If the provider supplies actual billing information, store it as actual cost.

If the platform calculates cost from token usage and a pricing table, mark it as estimated.

Example:

```json
{
  "cost": {
    "amount": 0.82,
    "currency": "USD",
    "type": "estimated",
    "pricing_version": "2026-09"
  }
}
```

Do not assume all providers use USD.

---

# 15. Session

A session represents a continuous development interaction.

Example:

```text
Session
    |
    +-- Prompt 1
    +-- Prompt 2
    +-- Prompt 3
    +-- Tool calls
    +-- File changes
    +-- Tests
```

Session fields:

```text
session_id
user_id
project_id
IDE
agent
started_at
ended_at
status
```

---

# 16. Trace

A trace represents one logical development task.

Example:

```text
Trace: Fix Auto PO retry problem

Prompt
   |
   +-- LLM call
   |
   +-- read file
   |
   +-- search code
   |
   +-- modify file
   |
   +-- run tests
   |
   +-- fix test
   |
   +-- git commit
```

Every event belonging to the same task should share:

```text
trace_id
```

This is critical for attribution.

---

# 17. Tool Call Tracking

Coding agents frequently invoke tools.

Capture:

```text
tool_call_id
trace_id
tool_name
started_at
completed_at
duration
status
input metadata
output metadata
```

Examples:

```text
read_file
write_file
edit_file
search
grep
terminal
shell
test
build
git
browser
database
```

Do not hard-code the tool list.

Allow custom tool names.

---

# 18. File Activity Tracking

Track file activity separately from LLM activity.

Supported operations:

```text
CREATE
READ
MODIFY
DELETE
RENAME
```

File metadata:

```text
file_id
project_id
trace_id
session_id
user_id
path
language
operation
timestamp
```

Example:

```json
{
  "event_type": "file_change",
  "file": {
    "path": "src/order/service.py",
    "language": "python",
    "operation": "modify"
  }
}
```

The system must not require a known programming language.

If language cannot be determined:

```text
language = unknown
```

---

# 19. Code Change / Diff Tracking

When a file is modified, capture the change where possible.

Recommended fields:

```text
file_path
before_hash
after_hash
lines_added
lines_removed
diff
change_source
```

Change source:

```text
AI
HUMAN
UNKNOWN
```

Do not assume every file change came from AI.

---

# 20. Change Attribution

The system should attempt to establish:

```text
Prompt
    ↓
Agent action
    ↓
File modification
```

using:

```text
trace_id
session_id
timestamp
agent event
file event
```

Example:

```text
Prompt #123
       |
       +---- Agent edit
                 |
                 +---- OrderService.java
                 +---- RetryConfig.java
```

The platform must not claim AI attribution if the evidence is insufficient.

Use:

```text
AI_ATTRIBUTED
HUMAN_ATTRIBUTED
UNKNOWN
```

when appropriate.

---

# 21. Human vs AI Changes

A developer may manually edit files after an AI agent changes them.

The system must distinguish:

```text
AI change
Human change
Unknown change
```

Example:

```text
10:00 AI modifies file
10:03 developer manually modifies file
10:10 AI modifies file
```

The timeline should preserve these separately.

---

# 22. Git Integration

Where Git is available, collect:

```text
commit
branch
checkout
merge
rebase
diff
status
```

For commits:

```text
commit_sha
author
timestamp
branch
files_changed
lines_added
lines_removed
```

The system must not require Git.

Non-Git projects must still work.

---

# 23. Local Collector

Provide an optional lightweight local collector.

Its responsibilities:

```text
receive local events
normalize events
buffer events
batch events
send events
retry failed requests
```

The collector must not block the coding agent.

If the server is unavailable:

```text
Coding Agent
     |
     X Platform unavailable
     |
Coding Agent continues normally
```

Events can be buffered locally and uploaded later.

---

# 24. Performance Requirements

The tracking system itself must have minimal overhead.

Target requirements:

```text
Normal idle CPU:
< 1%

Normal memory:
keep low; target < 50 MB for the local collector

Event capture:
non-blocking

Network:
asynchronous

Failure:
must never block coding activity

Startup:
fast

Shutdown:
graceful
```

These are targets, not reasons to compromise correctness.

Measure actual performance during implementation.

Do not add unnecessary background processes.

---

# 25. Technology Stack Requirements

The platform itself must NOT force customers to use a particular technology.

For example, a customer using:

```text
Java + Spring Boot
```

must be able to integrate.

A customer using:

```text
Python + FastAPI
```

must also be able to integrate.

A customer using:

```text
Node.js + TypeScript
```

must also be able to integrate.

Likewise:

```text
Go
Rust
C#
C++
PHP
Ruby
Kotlin
etc.
```

must be supported through the generic HTTP API.

---

# 26. SDK Strategy

SDKs are convenience layers, NOT the core integration mechanism.

Recommended SDKs may eventually include:

```text
Java SDK
Python SDK
TypeScript/JavaScript SDK
Go SDK
Rust SDK
C# SDK
```

But the first implementation must work without any SDK.

The REST API is the universal compatibility layer.

Do not delay core functionality because an SDK does not exist.

---

# 27. CLI Strategy

Provide an optional CLI.

Example:

```bash
aiobserve login
aiobserve init
aiobserve status
aiobserve doctor
```

The CLI should help configure integrations but must not be mandatory if a developer wants to use the REST API directly.

---

# 28. IDE Integration Strategy

IDE plugins should be thin adapters.

Example:

```text
VS Code Plugin
      |
      v
Local Collector
      |
      v
Platform
```

and:

```text
IntelliJ Plugin
      |
      v
Local Collector
      |
      v
Platform
```

Do not duplicate business logic in every IDE plugin.

---

# 29. Agent Integration Strategy

Use an adapter architecture:

```text
Agent
  |
  v
Agent Adapter
  |
  v
Standard Event
  |
  v
Platform
```

An adapter converts agent-specific events into the platform's standard event schema.

Do not create separate backend business logic for every agent.

---

# 30. Standard Event Types

Initially support:

```text
session.started
session.ended

prompt.created

llm.request.started
llm.request.completed
llm.request.failed

tool.started
tool.completed
tool.failed

file.read
file.created
file.modified
file.deleted
file.renamed

test.started
test.completed

git.commit
git.branch
git.checkout
```

The event system must allow future event types.

---

# 31. Event Idempotency

The API must handle duplicate events safely.

Every event must have:

```text
event_id
```

If the same event is submitted twice:

```text
event_id = evt_123
```

the backend must not count it twice.

This is required because clients may retry requests.

---

# 32. Offline / Network Failure

The local integration should tolerate network failure.

Example:

```text
Agent
  |
Collector
  |
Network unavailable
  |
Local queue
  |
Network restored
  |
Batch upload
```

Events should have:

```text
created_at
received_at
```

so delayed events can still be correctly placed in the timeline.

---

# 33. Security

API keys must:

* never be logged
* never be included in analytics
* support revocation
* support rotation
* be scoped
* be stored securely by clients

The server must validate:

```text
API key
project
organization
permissions
```

A project key should not automatically provide organization-admin access.

---

# 34. Privacy

The system may process sensitive source code and prompts.

Therefore support configuration such as:

```text
metadata only
prompt redaction
diff disabled
file content disabled
full tracking
```

The default should favor minimal data collection.

Do not require source-code content to calculate token usage.

---

# 35. Data Retention

Support configurable retention.

Example:

```text
7 days
30 days
90 days
1 year
custom
```

Different data types may eventually have different retention:

```text
usage metadata
prompts
diffs
tool output
full file snapshots
```

Do not implement complex retention policies in the first MVP unless required.

Design the data model so retention can be added later.

---

# 36. Dashboard

The dashboard should provide:

## Organization

```text
Total users
Total projects
Total tokens
Total cost
Total AI requests
```

## Project

```text
Total tokens
Total cost
Total prompts
Total sessions
Total traces
Files changed
Lines added
Lines removed
```

## User

```text
Prompts
Tokens
Cost
Sessions
Tasks
Files modified
Lines changed
```

## Agent

```text
Requests
Tokens
Cost
Tasks
Success/failure
```

## Model

```text
Input tokens
Output tokens
Cached tokens
Cost
Requests
```

---

# 37. Prompt Detail Page

A developer should be able to open a specific prompt and see:

```text
Prompt
User
Project
IDE
Agent
Model
Timestamp

Input tokens
Output tokens
Cached tokens
Reasoning tokens
Total tokens
Cost

Tools used

Files read
Files changed

Tests executed

Git activity
```

---

# 38. Trace Detail Page

A trace should show a chronological timeline:

```text
10:30:01 Prompt
10:30:03 LLM request
10:30:07 read_file
10:30:10 search
10:30:15 edit_file
10:30:20 edit_file
10:31:01 test
10:31:30 test completed
10:32:00 git commit
```

The developer should be able to understand the entire AI-assisted task.

---

# 39. Analytics

Support filters:

```text
date
project
user
IDE
agent
model
language
repository
branch
event type
```

Example query:

```text
Show all AI usage for Python projects during the last 30 days.
```

Another:

```text
Show all prompts made by User X.
```

Another:

```text
Show all files modified by AI for Project Y.
```

---

# 40. Cost Analytics

Provide:

```text
cost by user
cost by project
cost by agent
cost by model
cost by day
cost by programming language
```

Do not interpret high token usage as poor developer performance automatically.

Usage analytics and productivity analytics must remain separate concepts.

---

# 41. Usage Attribution

The system should answer:

```text
Who?
What did they ask?
When?
Where?
Which IDE?
Which agent?
Which model?
How many tokens?
How much cost?
Which tools?
Which files?
What changed?
What tests ran?
Which Git commit?
```

This is the core value proposition.

---

# 42. Example End-to-End Flow

Developer is working on a Python project in VS Code.

They use a coding agent and enter:

```text
"Fix the authentication retry issue."
```

The integration sends:

```text
user_id
project_id
session_id
trace_id
IDE
agent
model
prompt
```

The agent performs:

```text
read auth.py
read retry.py
search configuration
modify auth.py
modify retry.py
run tests
```

The platform receives events for each operation.

The final result becomes:

```text
User:
Gaurav

Project:
Authentication Service

IDE:
VS Code

Agent:
Agent X

Model:
Model Y

Prompt:
Fix authentication retry issue

Tokens:
42,381

Cost:
$0.82

Files read:
14

Files changed:
2

Lines added:
48

Lines removed:
17

Tests:
18 passed

Git commit:
abc123
```

The exact same process must work for a Java project, Rust project, TypeScript project, etc.

---

# 43. MVP Scope

Do NOT implement every possible feature immediately.

MVP must contain only:

### Authentication

* organization
* user
* project
* API key

### Event API

* event ingestion
* validation
* idempotency

### Core events

* prompt
* LLM request
* tool call
* file change
* session
* trace

### Usage

* token tracking
* cost calculation
* model/provider information

### Attribution

* user
* project
* session
* trace
* prompt
* file change

### Dashboard

* project overview
* user overview
* prompt detail
* trace detail
* token/cost analytics

### Integration

* generic REST API
* one example SDK
* one lightweight local collector if required

---

# 44. Explicitly DO NOT Build in MVP

Do NOT add these unless specifically requested later:

* Kubernetes
* microservices
* Kafka
* complex message brokers
* service mesh
* AI model hosting
* LLM proxying
* billing system
* enterprise SSO
* complex RBAC
* automatic AI quality scoring
* code-quality scoring
* developer performance scoring
* automatic code review
* full source-code indexing
* vector database
* RAG
* AI chat interface
* complicated workflow engine
* unnecessary cloud-provider-specific services

The first implementation should be small and functional.

---

# 45. Architecture Constraint

Prefer a modular monolith for the initial implementation.

Do NOT create multiple microservices unless there is a demonstrated requirement.

Logical modules may be:

```text
auth
projects
users
events
sessions
traces
usage
cost
files
git
analytics
```

These may initially exist inside one deployable backend.

Keep module boundaries clean so components can be separated later if required.

---

# 46. Backend Technology Selection

The implementation team may choose the backend technology based on performance, simplicity, maintainability, and team expertise.

Possible technologies include:

```text
Go
Rust
Java
Kotlin
Node.js / TypeScript
Python
C#
etc.
```

There is NO requirement that the backend be written in Go.

If Go is selected, use it because it satisfies the performance/lightweight requirements—not because the product requires Go.

Likewise, if another language is selected, the same API and behavior requirements must remain unchanged.

The technology choice must never leak into the public integration contract.

---

# 47. Frontend Technology

The dashboard may use:

```text
React + TypeScript
```

or another suitable lightweight web technology.

The frontend must communicate through documented APIs.

Do not couple the frontend directly to database models.

---

# 48. Database Requirements

The database must support:

```text
users
organizations
projects
repositories
api_keys
sessions
traces
events
prompts
llm_usage
tool_calls
file_changes
git_events
```

The implementation may use a relational database such as PostgreSQL.

For very high-volume analytics, an analytical store can be introduced later.

Do not introduce a separate analytical database in MVP unless actual measurements justify it.

---

# 49. API Versioning

All public APIs must be versioned.

Example:

```text
/api/v1/events
/api/v1/projects
/api/v1/users
/api/v1/sessions
/api/v1/traces
```

Do not expose unversioned public APIs.

---

# 50. API Documentation

Document:

```text
authentication
event schema
required fields
optional fields
error responses
retry behavior
idempotency
rate limits
examples
SDK examples
```

Provide examples for multiple technologies.

At minimum demonstrate:

```text
curl
JavaScript/TypeScript
Python
Java
```

This proves that the system is technology agnostic.

---

# 51. Error Handling

API errors must be structured.

Example:

```json
{
  "error": {
    "code": "INVALID_EVENT",
    "message": "project_id is required"
  }
}
```

Do not expose stack traces or internal implementation details to clients.

---

# 52. Rate Limiting

Support basic rate limiting.

It should be configurable by:

```text
organization
project
API key
```

Do not implement an extremely complex rate-limiting system for MVP.

---

# 53. Observability of the Platform

The platform itself should expose:

```text
request latency
event ingestion rate
failed events
duplicate events
queue/buffer size
database latency
API errors
CPU
memory
```

The monitoring platform must not become unobservable itself.

Use standard observability mechanisms where appropriate.

---

# 54. Testing Requirements

Write tests for:

### API

* authentication
* invalid API key
* invalid project
* event validation
* duplicate event
* malformed event

### Usage

* token aggregation
* cost calculation
* missing token fields
* different models
* different currencies

### Attribution

* prompt → trace
* trace → file change
* user → prompt
* project → activity

### Reliability

* network failure
* retry
* duplicate event
* delayed event

### Privacy

* metadata-only mode
* redaction
* disabled diff tracking

---

# 55. Non-Functional Requirements

The platform must prioritize:

```text
Low latency
Low memory usage
Low CPU overhead
Reliability
Security
Privacy
Extensibility
Technology independence
Backward compatibility
Observability
Simple deployment
```

Correctness is more important than premature optimization.

Do not add infrastructure merely because it is considered "production grade."

---

# 56. Definition of Done

The MVP is complete only when the following scenario works:

1. A user creates an organization.
2. The user creates a project.
3. The user generates an API key.
4. A client written in any supported technology can send events using HTTP.
5. The system identifies the project and user.
6. A session is created.
7. A trace is created.
8. A prompt is recorded.
9. LLM token usage is recorded.
10. Cost is calculated or recorded.
11. Agent and IDE information is recorded.
12. Tool calls are recorded.
13. File changes are recorded.
14. Changes can be associated with the trace when evidence exists.
15. Git information can be recorded when available.
16. Duplicate events do not create duplicate usage.
17. Network failures do not break the developer's coding workflow.
18. The dashboard displays the activity.
19. The user can inspect the complete trace.
20. The same architecture works regardless of whether the underlying project is Java, Python, TypeScript, Go, Rust, or another language.

---

# 57. Most Important Development Rule

Before implementing any feature, ask:

> Is this required by the current specification?

If not, do not add it.

Do not introduce:

* unnecessary abstractions
* unnecessary services
* unnecessary dependencies
* unnecessary infrastructure
* unnecessary database technologies
* unnecessary SDKs
* unnecessary configuration
* unnecessary UI pages

Prefer the smallest implementation that satisfies the requirement.

---

# 58. Final Product Concept

The final product should provide a unified view:

```text
                    ORGANIZATION
                         |
                       USERS
                         |
                       PROJECT
                         |
              +----------+----------+
              |                     |
             IDE                  AGENT
              |                     |
              +----------+----------+
                         |
                       SESSION
                         |
                        TRACE
                         |
        +----------------+----------------+
        |                |                |
      PROMPT          TOOL CALL       FILE CHANGE
        |                |                |
        +----------------+----------------+
                         |
                    LLM USAGE
                         |
              +----------+----------+
              |                     |
            TOKENS                 COST
                         |
                      GIT EVENT
```

The platform should ultimately answer one question extremely well:

> **For any project, show me who used AI, what they asked, which IDE/agent/model they used, how many tokens and how much cost were consumed, what tools were used, what files were changed, and how those changes relate to the AI-assisted task.**

Everything else should support this core objective.
