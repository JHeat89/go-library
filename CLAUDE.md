# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
go build ./...                  # build all packages
go test ./...                   # run all tests
go test ./logger -run TestLifecycle   # run a single test
gofmt -w . && go vet ./...      # format and vet before finishing work
```

Requires Go 1.26+. Module path: `github.com/JHeat89/go-library`.

## What this library is

A reusable structured-logging library for enterprise Go services (REST,
GraphQL, ETL, Kafka), plus an OAuth 2.0 token-acquisition package and a
secrets/parameter loader that log through the same base logger. Everything
builds on the base `logger` package; the other packages are thin
domain-specific layers over it.

## Architecture

**Core principle: stdlib-first, adapters optional.** The core packages
(`logger`, `requestid`, `rest`, `graphql`, `etl`, `events`, `oauth`,
`secrets`) have zero third-party dependencies — the engine is `log/slog`.
Only `graphql/gqlgen` imports gqlgen, only `oauth/redis` imports go-redis, and
only `secrets/aws` imports aws-sdk-go-v2, so consumers who don't use gqlgen,
Redis-backed token caching, or AWS-backed secrets never pull them in. New
integrations must follow this split: agnostic core, dependency-bearing
adapter in a subpackage.

**Request correlation flows through context.Context, not logger instances.**
- `logger.EnsureRequestID(ctx)` generates a UUIDv4 (via `requestid`) at the
  application edge; `rest`, `graphql`, `etl`, and `events` all call it for you.
- `logger/handler.go` defines `contextHandler`, an `slog.Handler` decorator
  that injects `requestId` from the context into every record. This is why all
  `Logger` methods take a `ctx` — even raw `slog` usage via `Logger.Slog()`
  stays correlated.
- Cross-service propagation: HTTP uses the `X-Request-ID` header
  (`rest.RequestIDHeader`); Kafka uses the `x-request-id` message header
  (`events.RequestIDHeader` + `events.OutgoingHeaders`).

**Lifecycle tracing** (`logger/lifecycle.go`): `Logger.StartRequest` stores a
`*Lifecycle` in the context; any layer can call
`logger.LifecycleFrom(ctx).Stage(...)` without plumbing. `Lifecycle.End` is
idempotent and attaches a `PerformanceSnapshot` (`logger/performance.go`,
Powertools-style: heap/GC/goroutines/coldStart) to the completion log line.

**Config and platform** (`logger/config.go`, `logger/platform.go`):
`logger.Init(Config)` is the single integration point for applications — it
sets the package default and `slog.SetDefault`. Platform (Lambda/ECS/K8s/
container/local) is auto-detected from env vars; format defaults to text on
local, JSON elsewhere. Built-in slog keys are renamed in `handler.go`
(`time`→`timestamp`, `msg`→`message`, lowercase `level`) — log aggregators
depend on these names.

## Conventions

- Log field keys are camelCase; domain packages namespace theirs with a prefix
  (`http.status`, `graphql.operation`, `etl.recordsProcessed`, `kafka.topic`).
- Edge packages return a finish/done func from their start call
  (`OperationStart`, `ConsumeStart`) rather than requiring paired calls.
- Tests assert on parsed JSON log output (see `logger/logger_test.go` for the
  `newTestLogger`/`lastLine` pattern) — reuse it rather than string-matching.
- Sensitive data: redaction is centralized in `logger/redact.go` (`Redactor`,
  applied handler-level via `Config.RedactKeys`/`RedactPII`/`RedactPatterns`).
  Key matching is case-insensitive and recursive; patterns scrub matched
  substrings only (partial redaction). `graphql.WithRedactedVariables`
  delegates to the same engine — never add package-local redaction logic.
  Exception: `oauth` never logs `AccessToken`/`ClientSecret`/`Password`/
  `SecurityToken`/`Raw`/response bodies by construction — it does not rely on
  the logger's redaction config for that guarantee, since a consumer could
  forget to configure `RedactKeys`.

## OAuth token acquisition (`oauth`, `oauth/redis`)

`oauth.New(base *logger.Logger, oauth.Config) (*oauth.Client, error)` acquires
OAuth 2.0 access tokens via `client_credentials` or Salesforce's
username/password flow (`Config.GrantType`). `Client.Token(ctx)` is
cache-aware (`Config.CacheRead`/`CacheWrite`, backed by `Config.Cache`);
`Client.Refresh(ctx)` always fetches fresh; `Client.Invalidate(ctx)` deletes
the cached entry. **There is no in-memory token cache** — every `Token` call
consults `Config.Cache` or fetches, because an external process may refresh
the cached token out-of-band; see the `oauth` package doc for the full
rationale (also: no singleflight). Cache read/write failures are Warn-logged
and fall through rather than failing the call. `oauth/redis` adapts go-redis
v9 (`oauth.TokenCache`) and is the only file in the module importing it.

## Secrets and parameter loading (`secrets`, `secrets/aws`)

`secrets.New(base *logger.Logger, src secrets.Source) *secrets.Loader` wraps
a `Source` (`Secret`/`Parameter`/`ParametersByPath`) with structured logging,
request-id correlation, and `Loader.SecretJSON` parsing for the common
Secrets Manager key/value shape. `secrets/aws` adapts aws-sdk-go-v2's
Secrets Manager and SSM Parameter Store clients to `secrets.Source` and is
the only file in the module importing the AWS SDK; the consumer builds the
`aws.Config` (e.g. via `config.LoadDefaultConfig`) and passes it to
`aws.New`, matching the consumer-owns-the-client precedent from `oauth/redis`.
Parameter reads always set `WithDecryption`; `ParametersByPath` is recursive
with full `NextToken` pagination. **There is no cache** — secrets and
parameters are meant to be loaded once at startup and held by the caller.
Like `oauth`, secret and parameter *values* are never logged, only
names/paths/source/duration.
