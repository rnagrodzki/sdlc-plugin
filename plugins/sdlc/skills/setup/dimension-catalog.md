# Review Dimension Catalog

Reference catalog of review dimensions organized by category. Each row maps a
project evidence signal to a dimension name and default severity. Only propose a
dimension when the corresponding evidence is found during the Step 1 scan.

## Core Dimensions (technical)

| Evidence found | Dimension | Severity |
| --- | --- | --- |
| Auth dirs, JWT/OAuth/session deps | `security-review` | high |
| ORM deps, migration files, SQL dirs | `data-integrity-review` | high |
| Route/controller/handler dirs, OpenAPI/Swagger files | `api-review` | high |
| Queue libs, worker dirs, async patterns, thread pools | `concurrency-review` | high |
| Cache libs (Redis, Memcached), service/repo layers | `performance-review` | medium |
| Test files present (`*.test.*`, `*.spec.*`) | `test-coverage-review` | medium |
| Multiple `.md` files, `docs/` directory | `documentation-review` | low |
| Docker, k8s, Terraform, CI/CD files | `infrastructure-review` | medium |
| UI components, CSS/SCSS, template files | `ui-review` | medium |
| Any project (always include) | `code-quality-review` | medium |

## Extended Dimensions (non-technical and cross-cutting)

| Evidence found | Dimension | Severity |
| --- | --- | --- |
| Mixed casing styles across files, ESLint naming rules configured | `naming-conventions-review` | low |
| JSDoc/docstring config, CHANGELOG.md, README quality signals | `documentation-quality-review` | low |
| `.github/workflows/`, `.circleci/`, `Jenkinsfile`, CI config | `ci-cd-pipeline-review` | medium |
| OpenAPI/Swagger/GraphQL schemas (`*.graphql`, `openapi.*`), `*.proto` files | `api-contract-review` | high |
| Lock files (`package-lock.json`, `yarn.lock`, `poetry.lock`), `.npmrc`, license-checking deps | `dependency-management-review` | medium |
| `.env*` files, `config/` directory, feature flag libs (LaunchDarkly, Unleash, ConfigCat) | `configuration-management-review` | medium |
| Error boundary files, custom error classes, retry/circuit-breaker patterns | `error-handling-review` | medium |
| UI components + a11y testing deps (`jest-axe`, `@axe-core/*`, `@testing-library/jest-axe`) | `accessibility-review` | medium |
| `i18n/`, `locales/`, `translations/` dirs, i18n lib deps (`i18next`, `react-intl`, `vue-i18n`) | `internationalization-review` | low |
| `migrations/` dir, Prisma/Alembic/Flyway/Liquibase files, `*.sql` migration scripts | `database-migrations-review` | high |
| Structured logging libs (`winston`, `pino`, `structlog`), OpenTelemetry deps | `logging-observability-review` | medium |
| `tsconfig.json` with `strict: true`, `.d.ts` files present | `type-safety-review` | medium |
| Redux/Zustand/Vuex/MobX/Pinia deps, `store/` or `state/` dirs | `state-management-review` | medium |
| `bin/` dir, `commander`/`yargs`/`meow`/`clap`/`cobra` deps | `cli-ux-review` | medium |
| `openspec/config.yaml` present, `openspec/changes/*/specs/*.md` delta spec files | `spec-compliance-review` | high |

## Project-type Dimensions (conditional on project structure)

| Evidence found | Dimension | Severity |
| --- | --- | --- |
| `packages/`/`apps/` dirs + workspace config (`lerna.json`, `pnpm-workspace.yaml`, `nx.json`, or `workspaces` in package.json) | `monorepo-governance-review` | medium |
| `plugins/` or `extensions/` dirs + manifest files (`plugin.json`, `manifest.json`) or hook registration patterns | `plugin-architecture-review` | medium |
| Package exports, `index.ts`/`index.js` barrel files, semver in package.json, `CHANGELOG.md` | `sdk-library-design-review` | high |
| `android/`/`ios/` dirs, React Native/Flutter/Capacitor deps | `mobile-app-review` | medium |
| DAG definitions, ETL scripts, `pipeline/` dirs, Spark/Airflow/Dagster deps | `data-pipeline-review` | high |
| Model files, `training/` dirs, ML libs (torch, tensorflow, sklearn) in requirements | `ml-ai-review` | medium |
| Docker Compose with multiple services, `services/` dir, API gateway config, contract test files | `microservices-review` | medium |
