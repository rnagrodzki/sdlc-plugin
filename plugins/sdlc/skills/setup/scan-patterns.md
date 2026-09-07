# Scan Patterns — Structural Signals

Glob patterns for detecting project structure signals. Run these in parallel using the Glob tool. Do NOT read file contents — directory/filename presence is the signal.

```text
**/middleware/**      → auth / request pipeline
**/auth/**           → authentication
**/routes/**         → HTTP routing
**/controllers/**    → MVC controllers
**/handlers/**       → request handlers
**/migrations/**     → database migrations
**/models/**         → data models
**/repositories/**   → data access layer
**/workers/**        → background workers
**/queues/**         → message queues
**/jobs/**           → scheduled jobs
**/components/**     → UI components
**/pages/**          → page components (Next.js, Nuxt, etc.)
**/views/**          → view templates
**/*.scss            → CSS/styling
**/terraform/**      → infrastructure as code
**/k8s/**            → Kubernetes manifests
**/Dockerfile        → containerization
**/*.test.*          → test files
**/*.spec.*          → test files
docs/                → documentation directory

**/i18n/**           → internationalization
**/locales/**        → internationalization
**/translations/**   → internationalization
**/.env*             → configuration management
**/config/**         → configuration management
**/feature-flags/**  → configuration management
**/*.graphql         → API contract (GraphQL)
**/*.proto           → API contract (gRPC/protobuf)
**/openapi.*         → API contract (OpenAPI)
**/swagger.*         → API contract (Swagger)
**/*.schema.*        → config/type schemas
**/a11y/**           → accessibility
**/cypress/**        → E2E testing
**/playwright/**     → E2E testing
**/packages/*/       → monorepo workspace
**/apps/*/           → monorepo workspace
**/libs/*/           → monorepo shared libs
**/plugins/*/        → plugin architecture
**/extensions/*/     → plugin architecture
**/*.d.ts            → TypeScript type definitions
**/tsconfig*.json    → TypeScript project config
**/Jenkinsfile       → CI/CD
**/.circleci/**      → CI/CD
**/android/**        → mobile app
**/ios/**            → mobile app
**/model*/**         → ML/AI
**/dags/**           → data pipeline (Airflow)
**/pipeline*/**      → data pipeline
**/store/**          → state management
**/state/**          → state management
**/bin/**            → CLI entry points
```
