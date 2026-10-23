# Repository architecture

Paths in this reference are relative to the repository root. The two lint configurations are authoritative for permitted imports; inspect them before changing boundaries.

## DDD and the Go adaptation

Domain-driven design organizes code around business meaning and a shared vocabulary. Entities retain identity as their attributes change; value types describe business data. A bounded context defines the scope in which a model and its vocabulary apply. Use these concepts to identify ownership and invariants. [Eric Evans' DDD Reference](https://www.domainlanguage.com/wp-content/uploads/2016/05/DDD_Reference_2015-03.pdf)

Here, `Process` and `ProcessDefinition` are identity-bearing models; `Reference`, parameters, and typed states express related business data. Shared models live in `internal/core/domain`. Follow their existing functional-option constructors and `Validate` methods for intrinsic constraints. Application use cases coordinate lifecycle decisions and persistence. Apply the skill's mutable-pointer conventions to domain models; DDD terminology does not override those conventions.

Onion architecture keeps source dependencies directed toward business rules. An application calls an injected repository interface at runtime while infrastructure provides a compatible implementation. This separates business behavior from storage and transport mechanisms. [Jeffrey Palermo's Onion Architecture](https://jeffreypalermo.com/2008/07/the-onion-architecture-part-1/)

```text
service composition root -> app + infra + domain
inbound infra adapter    -> app -> domain
outbound infra adapter   -> domain; implements app-owned interfaces
```

Go satisfies interfaces implicitly: a concrete adapter can implement an app-owned interface without importing its package. Define repository interfaces beside the application feature that consumes them. Inbound adapters can define small interfaces for the use cases they consume. Construct concrete adapters and inject them through `New...` constructors in service roots.

## Modules and package ownership

The application uses one root Go module, `go-software-factory`. `internal/core`, `internal/engine`, and `internal/controlplane` are logical components within it. These code ownership boundaries do not automatically imply separate deployments or DDD bounded contexts. The lint tools have their own modules under `lint/`.

| Location | Responsibility |
| --- | --- |
| `cmd/<binary>/main.go` | Executable entrypoint and service lifecycle. |
| `internal/<service>/<service>.go` | Composition root: load configuration, construct adapters and use cases, expose the service's entry points, and assemble lifecycle components. |
| `internal/<service>/domain/` | Service-owned business models and invariants when needed. Reuse deliberately shared models from `core/domain`. |
| `internal/<service>/app/<feature>/` | Named use cases, application inputs, workflow rules, and consumer-owned dependency interfaces. |
| `internal/<service>/infra/<adapter>/` | HTTP handlers, storage implementations, authentication adapters, scheduler jobs, and other external mechanisms. |
| `internal/<service>/config/` | Environment loading, configuration validation, and settings for composition. |
| `internal/core/` | Shared domain models and explicitly mapped supporting packages. |

Use business feature names for application packages, such as `process` and `processdefinition`. Follow the existing `CreateProcessUseCase` / `create_process_usecase.go` naming and adjacent test convention. Create only the layers a component needs; `controlplane` currently uses configuration and web infrastructure without its own application or domain layer.

## Dependency rules

Every internal import must satisfy both configurations. A layer allowance cannot authorize a forbidden component dependency, and a component allowance cannot authorize an outward layer dependency.

[Layer rules](../../../../lint/architecture-layers.yml):

| Component | Allowed internal layer components |
| --- | --- |
| `domain` | `domain`, `validator` |
| `app` | `app`, `domain`, `validator` |
| `infra` | `infra`, `app`, `domain`, `validator`, `core-helpers` |
| `configuration`, `bootstrap` | Any project component, subject to service boundaries. |
| `validator` | `validator` |
| `core-helpers` | `core-helpers`, `domain`, `validator`, `configuration` |

`bootstrap` covers service-root packages. `validator` means `core/validator`, which wraps the validation library. `core-helpers` means the mapped `core/dsl`, `core/logger`, `core/scheduler`, and `core/service` packages; `core/config` is configuration and `core/domain` is domain. Standard-library imports are allowed. Direct third-party imports are prohibited in domain packages and permitted in the other mapped components. Keep transport and storage mechanisms in infrastructure.

[Service rules](../../../../lint/architecture-services.yml):

| Component | Allowed internal components |
| --- | --- |
| `core` | `core` |
| `engine` | `engine`, `core` |
| `controlplane` | `controlplane`, `engine`, `core` |

For example, an engine application use case may import `core/domain` or `core/validator`, but cannot import `core/scheduler` or `engine/infra/storage/sqlite`. A control-plane handler may depend on engine application behavior; engine cannot import control-plane packages.

Both checks inspect imports with `deepScan: false`. Layer rules exclude all `*_test.go` files so tests can wire adapters and use cases; service boundaries still apply to tests. Passing import lint does not verify domain invariants or runtime behavior.

## Adding packages and services

Every new Go package under `internal/` must have defined architecture dependency rules in both `lint/architecture-layers.yml` and `lint/architecture-services.yml`. Before adding a package, verify which components and dependency rules cover it in both configurations. Existing recursive rules may cover new packages within a service; register every new service in the service configuration. If a package is not covered, define its component and allowed dependencies in the applicable configuration before adding imports. Verify the mappings with `go-arch-lint mapping` for both configurations and run `make lint`.

## Concrete examples

- [Domain model and validation](../../../../internal/core/domain/process.go).
- [Application repository contract](../../../../internal/engine/app/processdefinition/processdefinition_usecases.go).
- [Storage adapter wiring](../../../../internal/engine/infra/storage/storage.go).
- [Engine composition](../../../../internal/engine/engine.go) and [control-plane composition](../../../../internal/controlplane/controlplane.go).
- [Inbound consumer interfaces](../../../../internal/controlplane/infra/web/handler/processdefinition/processdefinition.go).
