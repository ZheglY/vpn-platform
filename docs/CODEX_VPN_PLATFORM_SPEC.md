# Техническое задание для Codex: микросервисная VPN-платформа на Go

## 0. Как использовать этот документ

Этот файл — основной контекст и поэтапный план реализации. Перед началом работы:

1. Создай пустой Git-репозиторий и положи этот файл в `docs/CODEX_VPN_PLATFORM_SPEC.md`.
2. Запусти Codex из корня репозитория в Plan mode.
3. Передай ему раздел **«Мастер-промпт»** и попроси сначала выполнить только Этап 0.
4. Каждый последующий этап запускай отдельной задачей. После этапа обязательно проверяй diff, тесты и документацию.
5. Не проси реализовать весь проект за один проход: архитектура большая, а безопасная реализация требует проверяемых инкрементов.

Документ описывает целевую production-архитектуру. Локально все компоненты запускаются через Docker Compose, но границы сервисов должны позволять раздельный деплой.

---

## 1. Мастер-промпт для Codex

```text
Ты работаешь над production-grade портфолио-проектом: микросервисной платформой продажи и выдачи VPN-доступа через Telegram-бота. Клиентское приложение пользователя — Happ. Happ не является VPN-провайдером: платформа должна выдавать секретную subscription URL, совместимую с Happ, а VPN-трафик должен проходить через наши арендованные узлы с Xray-core. Базовый протокол первой версии — VLESS + REALITY.

Главные требования:
- Go, стандартный net/http для HTTP-транспортов;
- zap для структурированного логирования;
- pgx/v5 и PostgreSQL; никаких ORM;
- Kafka для асинхронных доменных событий;
- Redis только для эфемерных данных, кэша, rate limiting и коротких блокировок; Redis не является source of truth;
- отдельный Dockerfile для каждого сервиса и Docker Compose для локального полного окружения;
- строгие границы bounded context, database-per-service, transactional outbox/inbox, идемпотентность;
- OpenAPI для HTTP API, AsyncAPI или эквивалентная документация для Kafka-событий;
- миграции, unit/integration/contract/e2e-тесты, observability, security hardening, CI;
- секреты и VPN subscription token нельзя писать в логи, ошибки, метрики или traces;
- все внешние операции (платежи, provision/revoke, Kafka handlers, Telegram updates) должны быть безопасны при повторном выполнении;
- не использовать float для денег;
- не добавлять абстракции «на будущее» без конкретного сценария использования;
- не создавать общий пакет с доменными моделями нескольких сервисов;
- не обращаться напрямую к базе другого сервиса;
- Kafka не заменяет синхронный HTTP-вызов, когда вызывающей стороне нужен немедленный результат.

Перед кодом:
1. Прочитай весь docs/CODEX_VPN_PLATFORM_SPEC.md и существующий AGENTS.md.
2. Исследуй репозиторий и актуальные официальные спецификации интеграций.
3. Зафиксируй неизвестные продуктовые решения в docs/adr/ как вопросы или ADR. Не выдумывай юридические, налоговые и provider-specific значения.
4. Составь детальный план только текущего этапа: файлы, миграции, API/events, тесты, риски, rollback.
5. Если решение меняет публичный API, схему события, модель безопасности, платежную семантику или границу сервиса — остановись и запроси подтверждение.

Во время реализации:
- делай небольшие завершенные изменения;
- сохраняй обратную совместимость опубликованных событий; каждое событие имеет schema_version;
- используй context.Context, таймауты, graceful shutdown и ограничение размера request body;
- валидируй данные на transport boundary, а бизнес-инварианты — в application/domain layer;
- оборачивай ошибки с контекстом, но не раскрывай секреты;
- используй UTC внутри системы; время хранить как timestamptz;
- UUID генерировать на стороне приложения; публичные bearer-токены генерировать CSPRNG;
- SQL должен быть явным, параметризованным и проверяемым;
- изменения БД выполнять forward-only миграциями; destructive migration делить на expand/migrate/contract;
- для каждой внешней зависимости предусмотреть timeout, retry только для безопасных ошибок, exponential backoff с jitter и circuit-breaking/ограничение конкурентности там, где это оправдано;
- не логировать Telegram init/update payload целиком, Authorization/Cookie, YooKassa credentials, webhook body целиком, VLESS UUID, REALITY private key, subscription URL/token, IP-назначения пользователя и содержимое трафика;
- обновлять README, OpenAPI/AsyncAPI и runbook вместе с поведением.

После реализации текущего этапа:
1. Запусти formatter, vet, linter, unit-тесты, race tests там, где применимо, integration/contract tests текущего этапа, vulnerability scan и Docker build.
2. Проверь git diff как code reviewer: correctness, security, concurrency, idempotency, SQL transactions, leakage of secrets, backward compatibility.
3. Исправь найденное и повтори проверки.
4. Выдай отчет: что реализовано, какие решения приняты, какие команды прошли, что не удалось проверить, риски и следующий этап.
5. Не объявляй этап завершенным при падающих обязательных проверках или незакрытых критериях приемки.

Сейчас выполни только указанный пользователем этап. Не начинай следующий этап автоматически.
```

---

## 2. Цель продукта

Платформа должна позволять:

- зарегистрировать пользователя по Telegram ID;
- показать тарифы и условия;
- создать заказ и платеж через YooKassa;
- надежно подтвердить платеж по webhook и контрольному запросу к провайдеру;
- активировать или продлить подписку;
- назначить доступ к одному или нескольким VPN-узлам;
- выдать пользователю секретную URL-подписку для Happ;
- автоматически отозвать доступ после окончания подписки или возврата;
- показать статус подписки, срок действия, устройства и доступные локации;
- обеспечить операторам безопасные административные функции и аудит;
- отслеживать здоровье и загрузку узлов без сбора истории посещений и содержимого трафика.

### Роли

- **Customer** — пользователь Telegram-бота.
- **Operator** — поддержка: просмотр минимально необходимого состояния, повтор безопасных операций, блокировка доступа.
- **Admin** — управление тарифами, узлами и ролями.
- **Node agent** — доверенный машинный субъект на VPN-сервере.
- **Payment provider** — YooKassa; архитектура должна поддерживать дополнительные адаптеры.

### Явно не входит в первый production milestone

- собственный VPN-клиент;
- хранение содержимого трафика, DNS-истории и посещенных адресов;
- криптовалютные платежи;
- автоматическая закупка VPS у провайдеров;
- сложный динамический выбор маршрута по каждому соединению;
- полноценная multi-tenant SaaS-модель;
- автосписания до отдельного юридического и продуктового решения;
- Kubernetes до появления реальной эксплуатационной необходимости.

---

## 3. Ключевые архитектурные решения

### 3.1 Модель репозитория

Использовать микросервисы в монорепозитории. Это не монолит: каждый сервис имеет собственный binary, bounded context, конфигурацию, миграции, Docker image, API и владельца данных. Монорепозиторий упрощает портфолио, CI, локальный запуск и согласованное изменение контрактов.

### 3.2 Синхронное и асинхронное взаимодействие

- HTTP/JSON — команды и запросы, на которые нужен немедленный ответ.
- Kafka — доменные события, фоновые процессы, интеграция без временной связанности.
- Нельзя строить распределенную транзакцию и нельзя считать публикацию Kafka-сообщения частью PostgreSQL-транзакции без outbox.
- События означают уже случившийся факт (`payment.succeeded`), а не RPC-команду в маскировке.
- Для фоновых команд (`access.provision.request`) явно определить владельца, retry policy и terminal failure.

### 3.3 Данные

Логически — database-per-service. В локальном Compose допустим один экземпляр PostgreSQL, но отдельные базы или схемы, отдельные DB users и отсутствие cross-schema joins. В production базы можно физически разделить без изменения кода.

### 3.4 VPN data plane и control plane

- **Data plane:** Xray-core на каждом VPN-узле обрабатывает пользовательский трафик.
- **Node agent:** маленький Go-daemon на каждом узле применяет desired state, проверяет конфигурацию, делает атомарное обновление и сообщает агрегированное состояние.
- **Control plane:** provisioning-service выбирает узел и общается с node-agent по mTLS через приватную management network, например отдельную WireGuard-сеть.
- VPN-узлы не подключаются напрямую к общей Kafka и PostgreSQL через публичный интернет.
- При потере control plane текущие активные подключения продолжают работать по last-known-good config.

### 3.5 Happ

Happ получает не «ключ от Happ», а bearer-секрет — URL вида `https://sub.example.com/s/{token}`. Endpoint возвращает список совместимых URI, сначала `vless://...`, и служебные заголовки/параметры Happ. Сам токен:

- минимум 256 бит энтропии от `crypto/rand`;
- хранится в БД только как hash/HMAC lookup value;
- показывается пользователю только в полном URL;
- ротируется и отзывается;
- полностью удаляется/маскируется в access logs, traces, ошибках и аналитике;
- не передается в query string, если можно использовать path segment;
- считается скомпрометированным при раскрытии истории браузера, Telegram или reverse-proxy logs.

Опциональное ограничение устройств через Happ HWID/limited links реализовать отдельным этапом после проверки документации и privacy-модели. Нельзя полагаться только на `User-Agent` или IP для идентификации устройства.

---

## 4. Состав системы

| Компонент | Ответственность | Собственные данные |
|---|---|---|
| `edge` (Caddy/Traefik/Nginx) | TLS, routing, coarse rate limit, security headers; редактирование логов | Нет доменных данных |
| `telegram-bot` | Telegram update ingestion, UX/FSM, команды пользователя, deep links | Redis FSM; durable состояние не хранит |
| `identity-service` | Telegram identity, профиль, статус блокировки, согласия, роли | users, telegram_identities, consents, roles |
| `catalog-service` | Тарифы, цены, периоды, доступные локации, публикация тарифов | plans, plan_prices, plan_locations |
| `billing-service` | Orders, YooKassa payments/refunds, provider adapters, webhook inbox | orders, payments, refunds, provider_webhooks, outbox |
| `subscription-service` | Entitlement и жизненный цикл подписки, продление, grace period | subscriptions, subscription_periods, outbox/inbox |
| `access-service` | Публичная subscription URL, token rotation, формирование Happ body | access_credentials, tokens, endpoint snapshots |
| `provisioning-service` | Реестр и capacity узлов, allocation, desired state, reconcile | nodes, allocations, operations, health snapshots |
| `notification-service` | Отправка Telegram-уведомлений по событиям, шаблоны, retry | deliveries, templates, inbox |
| `node-agent` | Локальное применение Xray clients/config, health и usage aggregates | last-known-good state, operation journal |

### Почему сервисы разделены именно так

- Billing не активирует Xray напрямую: деньги и сетевой доступ — разные bounded context.
- Subscription владеет правом доступа, access-service — способом доставки credential, provisioning — фактическим размещением на узлах.
- Telegram-бот не является источником истины и может быть заменен web/mobile frontend.
- Node agent изолирует platform control plane от особенностей Xray и ОС.

### Допустимое объединение только на ранней стадии

Если ресурсов для локальной разработки не хватает, `catalog-service` можно временно запускать в одном deployment с `billing-service`, а `notification-service` — с ботом, но код, схемы и интерфейсы оставить раздельными. Остальные границы не объединять.

---

## 5. Рекомендуемая структура монорепозитория

```text
.
├── AGENTS.md
├── README.md
├── Makefile
├── go.mod
├── .golangci.yml
├── .editorconfig
├── .env.example
├── compose.yml
├── services/
│   ├── identity/
│   │   ├── cmd/
│   │   │   └── identity-service/
│   │   │       └── main.go
│   │   ├── internal/{domain,application,repository,transport}/
│   │   ├── migrations/
│   │   ├── openapi/
│   │   └── Dockerfile
│   └── ...
├── platform/
│   ├── postgres/
│   ├── kafka/
│   ├── redis/
│   ├── edge/
│   ├── observability/
│   └── xray/
├── contracts/
│   ├── http/
│   ├── events/
│   └── asyncapi/
├── internal/platform/
│   ├── config/
│   ├── logging/
│   ├── httpserver/
│   ├── httpclient/
│   ├── postgres/
│   ├── kafka/
│   ├── outbox/
│   ├── observability/
│   └── cryptoutil/
├── deploy/
│   ├── compose/
│   ├── systemd/
│   └── ansible/
├── docs/
│   ├── architecture.md
│   ├── threat-model.md
│   ├── privacy.md
│   ├── runbooks/
│   ├── adr/
│   └── diagrams/
├── scripts/
└── .github/workflows/
```

`internal/platform` содержит только технические примитивы: logger factory, server lifecycle, Kafka envelope, outbox runner. Там запрещены `User`, `Subscription`, `Payment` и другие общие доменные сущности.

Для первой версии используется один корневой `go.mod`. `go.work` и отдельные Go-модули не требуются до появления конкретной необходимости. Service binaries располагаются внутри `services/<service>/cmd/<binary>/`, чтобы правила Go `internal` позволяли импортировать собственный service `internal` и не требовали корневого `cmd/`.

Для каждого сервиса зависимости направлены внутрь:

```text
transport -> application -> domain
repository adapter -> application/domain ports
external provider adapter -> application port
```

Domain не импортирует pgx, Kafka, net/http, zap, Telegram SDK или YooKassa SDK.

---

## 6. Технологический стандарт

- Использовать актуальную стабильную версию Go на момент начала реализации и закрепить точную версию в `go.mod`, CI и Docker image.
- HTTP server/router: стандартный `net/http` и `http.ServeMux`. Middleware писать компактно и тестировать.
- PostgreSQL: `github.com/jackc/pgx/v5`, `pgxpool`, явный SQL. При желании допускается генерация типобезопасного кода через sqlc, но runtime остается pgx.
- Миграции: `golang-migrate/migrate` или `goose`; выбрать одно и записать ADR.
- Logging: `go.uber.org/zap`; JSON в production, console encoder локально.
- Kafka: зрелый pure-Go клиент (`franz-go` предпочтителен) с manual commit после успешной обработки/inbox persistence.
- Redis: `go-redis/v9`.
- Telegram: тонкий адаптер вокруг поддерживаемой библиотеки или Bot API через net/http; домен от SDK не зависит.
- Metrics: Prometheus/OpenMetrics; tracing: OpenTelemetry; dashboards: Grafana; local traces: Tempo; logs: Loki или stdout collector.
- API description: OpenAPI 3.1; events: JSON Schema + AsyncAPI.
- Configuration: env vars, строгая валидация при старте, секреты через `_FILE`/secret mount или Vault-compatible provider в production.
- Quality: `gofmt`, `go vet`, `golangci-lint`, `govulncheck`, race detector, Trivy for images/IaC, Gitleaks.
- Testing: standard `testing`, Testcontainers-Go для integration tests, mock/fake только на портах.

Не фиксировать версии библиотек из этого документа: Codex должен проверить актуальные стабильные версии и changelog, затем pin конкретные версии в репозитории.

---

## 7. Доменные модели и схемы данных

Ниже — минимальная модель. Названия могут уточняться ADR, но инварианты обязательны.

### 7.1 Identity DB

`users`

- `id uuid primary key`
- `status` (`active`, `blocked`, `deleted`)
- `locale`, `timezone` nullable
- `created_at`, `updated_at`, `deleted_at`

`telegram_identities`

- `user_id uuid unique`
- `telegram_user_id bigint unique not null`
- username/display name — nullable и не используется как идентификатор
- `first_seen_at`, `last_seen_at`

`consents`

- `id`, `user_id`, `document_type`, `document_version`, `accepted_at`, `source`

`admin_roles`

- отдельная модель ролей; Telegram ID из env не является долгосрочной RBAC-моделью.

### 7.2 Catalog DB

`plans`: immutable ID, status, duration, traffic/device/location policy, display metadata.

`plan_prices`: `amount_minor bigint`, `currency char(3)`, `valid_from`, `valid_to`. Деньги никогда не хранить в float.

`plan_locations`: доступные регионы/группы узлов.

Опубликованный тариф нельзя изменять так, чтобы история заказов изменилась задним числом. В заказ копируется price/product snapshot.

### 7.3 Billing DB

`orders`

- `id`, `user_id`, `plan_id`
- `amount_minor`, `currency`, `plan_snapshot jsonb`
- `status`: `created`, `payment_pending`, `paid`, `canceled`, `refunded`
- `idempotency_key unique`, timestamps

`payments`

- internal ID, order ID, provider, provider_payment_id unique
- provider idempotency key unique
- expected amount/currency
- status state machine: `created -> pending -> succeeded|canceled`; terminal transition назад запрещен
- confirmation URL хранить только столько, сколько нужно; не логировать
- raw provider response хранить лишь при доказанной необходимости, с redaction и retention

`provider_webhooks`

- provider, dedupe key/payload hash, received_at, processing status, attempt, last_error_redacted
- unique constraint для идемпотентности

`refunds`

- payment ID, provider refund ID, amount, reason, status, idempotency key

`outbox_events`: стандартная outbox-таблица.

### 7.4 Subscription DB

`subscriptions`

- `id`, `user_id`, `plan_id`
- `status`: `pending`, `active`, `grace`, `expired`, `suspended`, `revoked`
- `current_period_start`, `current_period_end`
- `version bigint` для optimistic locking
- запрет более одной несовместимой active subscription по выбранной продуктовой модели

`subscription_periods`

- immutable ledger периодов: source order/payment, start/end, granted_at, revoked_at.
- повтор `payment.succeeded` не создает второй период.

`processed_events`/`inbox` с уникальным `event_id`.

Правило продления: если подписка активна, новый период начинается с текущего `current_period_end`; если истекла — с момента подтвержденного платежа. Формализовать тестами на boundary time.

### 7.5 Access DB

`access_credentials`

- `id`, `subscription_id`, `status`, `credential_version`
- Xray client UUID encrypted at rest или воспроизводимо отделен от публичного subscription token
- `created_at`, `revoked_at`

`subscription_tokens`

- `id`, `credential_id`, `token_lookup_hash unique`, `status`, `expires_at`, `rotated_from`, `last_used_at`
- plaintext token не сохраняется
- rotation может иметь короткое контролируемое overlap window

### 7.6 Provisioning DB

`nodes`

- `id`, region, public endpoint, management identity, status
- capacity limits, protocol capabilities, agent version
- public key material допустим; private keys отсутствуют

`allocations`

- `id`, access credential, node, protocol, status
- unique `(credential_id, node_id)`

`operations`

- operation id/idempotency key, type, desired revision, state, attempt, next_retry_at, redacted error

`node_health_snapshots`

- только агрегаты: heartbeat, load, active clients count, bandwidth totals, config revision.

Не хранить список посещенных сайтов, DNS-запросы и packet payloads.

---

## 8. Контракты Kafka

### 8.1 Envelope

```json
{
  "event_id": "uuid",
  "event_type": "billing.payment.succeeded",
  "schema_version": 1,
  "occurred_at": "RFC3339 UTC",
  "producer": "billing-service",
  "correlation_id": "uuid",
  "causation_id": "uuid-or-null",
  "aggregate_type": "payment",
  "aggregate_id": "uuid",
  "partition_key": "stable-aggregate-or-user-id",
  "data": {}
}
```

Правила:

- ключ партиции сохраняет порядок событий одного aggregate;
- consumers at-least-once и всегда идемпотентны;
- event ID глобально уникален;
- PII и секреты в событиях минимальны; subscription token и VPN private keys запрещены;
- backward-compatible добавление поля допустимо; удаление/смена смысла — новая schema version;
- schema validation в CI и contract tests;
- DLQ не является свалкой: alert, reason, replay tool и runbook обязательны;
- retention и число partitions задокументировать;
- outbox publisher использует `FOR UPDATE SKIP LOCKED`, контролируемые batch/retry и метрику возраста oldest event.

### 8.2 Минимальные topics/events

| Topic/event | Producer | Consumers | Смысл |
|---|---|---|---|
| `identity.user.registered.v1` | identity | notification/analytics optional | Создан пользователь |
| `billing.payment.succeeded.v1` | billing | subscription, notification | Платеж окончательно подтвержден |
| `billing.payment.canceled.v1` | billing | notification | Платеж отменен |
| `billing.refund.succeeded.v1` | billing | subscription, notification | Подтвержден возврат |
| `subscription.activated.v1` | subscription | access, notification | Entitlement активирован |
| `subscription.extended.v1` | subscription | access, notification | Период продлен |
| `subscription.expired.v1` | subscription | access, notification | Период закончился |
| `subscription.revoked.v1` | subscription | access, notification | Доступ отозван |
| `access.provision.request.v1` | access | provisioning | Требуется применить credential; содержит только credential ID, operation ID и revision |
| `access.revoke.request.v1` | access | provisioning | Требуется убрать credential; содержит только credential ID, operation ID и revision |
| `access.provision.succeeded.v1` | provisioning | access | Основной узел применил доступ; failover может быть degraded |
| `access.provision.failed.v1` | provisioning | access, ops | Terminal или требующая внимания ошибка provisioning |
| `access.revoke.succeeded.v1` | provisioning | access, notification | Credential удален со всех назначенных узлов |
| `access.revoke.failed.v1` | provisioning | access, ops | Terminal или требующая внимания ошибка revoke |
| `access.ready.v1` | access | notification, telegram-bot | VPN-доступ готов к одноразовой выдаче URL пользователю; URL/token в событии отсутствует |

Команды и события можно развести по topics, но naming convention должен быть единообразным и описан ADR.

---

## 9. Основные пользовательские потоки

### 9.1 Регистрация

1. Telegram webhook получает update, проверяет secret token header и dedupe по `update_id`.
2. Бот вызывает idempotent `PUT /internal/v1/telegram-users/{telegram_user_id}`.
3. Identity создает/возвращает пользователя.
4. Бот показывает меню; username не используется для authorization.

### 9.2 Покупка

1. Бот получает опубликованные тарифы из catalog-service.
2. Пользователь подтверждает тариф и условия.
3. Billing создает immutable order snapshot с client idempotency key.
4. Billing вызывает YooKassa Create Payment с уникальным `Idempotence-Key`, ожидаемой суммой, валютой и безопасной internal metadata.
5. Confirmation URL возвращается боту; секреты провайдера остаются только в billing-service.
6. Повторное нажатие не создает второй order/payment.

### 9.3 Webhook YooKassa

1. Edge применяет TLS, allowlist/rate limits по актуальным рекомендациям провайдера, но это не единственная проверка.
2. Billing ограничивает body, парсит strict JSON, сохраняет dedupe record/inbox.
3. Перед выдачей услуги billing получает payment по YooKassa API и сверяет provider payment ID, status, amount, currency, shop/account и order metadata.
4. В одной PostgreSQL-транзакции переводит payment/order в допустимое состояние и пишет outbox event.
5. Быстро возвращает корректный 2xx; долгие действия выполняются асинхронно.
6. Повторный, опоздавший или переставленный webhook безопасен.
7. Периодический reconciler проверяет зависшие `pending` платежи; webhook не является единственным источником восстановления.

### 9.4 Активация и provisioning

1. Subscription consumer принимает `payment.succeeded` через inbox и создает/продлевает entitlement.
2. Access consumer создает credential, но не создает публичный subscription token до завершения provisioning.
3. Access публикует `access.provision.request.v1`; событие содержит только credential ID, operation ID и revision.
4. Provisioning по mTLS запрашивает у access-service минимальный credential material, затем вызывает node-agent по mTLS с operation ID и desired revision.
5. Agent валидирует вход, применяет конфигурацию идемпотентно, выполняет Xray config test, atomic swap/reload и сохраняет last-known-good.
6. После успешного primary allocation access-service публикует `access.ready.v1`; failover failure переводит состояние в `degraded`.
7. Бот уведомляет пользователя, что VPN готов, и по нажатию синхронно вызывает authenticated endpoint issue/rotate URL. Access-service генерирует token, сохраняет только hash/HMAC и один раз возвращает URL. Kafka-событие никогда не содержит сам URL/token.

### 9.5 Получение подписки Happ

1. `GET /s/{token}` идет напрямую в access-service через отдельный hostname.
2. Token хешируется и ищется constant-time-compatible способом; проверяются token, subscription и allocation state.
3. Активная подписка возвращает `text/plain; charset=utf-8`, совместимые `vless://` URI и проверенные Happ headers (`profile-title`, `subscription-userinfo`, support URL и др.).
4. Истекшая/отозванная ссылка возвращает единообразный ответ без утечки существования пользователя; стратегия HTTP-кода и UX фиксируется ADR.
5. `Cache-Control: no-store`; proxy/CDN caching отключен; URL и path не попадают в access logs.
6. Генератор URI покрыт golden/compatibility tests по официальной документации Happ/Xray.

### 9.6 Истечение и отзыв

1. Subscription scheduler использует DB time/UTC и выбирает записи небольшими lock-safe batches.
2. Переход в expired/revoked и outbox event атомарны.
3. Access инвалидирует выдачу subscription document и запрашивает revoke.
4. Provisioning удаляет client UUID со всех назначенных узлов, повторяя операцию до успеха.
5. Возврат денег не должен автоматически означать мгновенный отзыв без явно определенной business policy; политика фиксируется ADR и тестами.

---

## 10. HTTP API — обязательный минимум

Все internal endpoints закрыты mTLS/service identity или private network + authenticated service credentials. Нельзя доверять только заголовку `X-Internal`.

### Identity

- `PUT /internal/v1/telegram-users/{telegram_id}` — idempotent upsert/register.
- `GET /internal/v1/users/{id}`.
- `POST /internal/v1/users/{id}/block` — admin-only, audited.

### Catalog

- `GET /v1/plans?channel=telegram&locale=...` — только published планы.
- Admin CRUD — отдельный authenticated surface, optimistic concurrency.

### Billing

- `POST /internal/v1/users/{user_id}/orders` с `Idempotency-Key`; вызывается только `telegram-bot` по mTLS после проверки Telegram update.
- `POST /internal/v1/users/{user_id}/orders/{id}/payments` с `Idempotency-Key`; вызывается только `telegram-bot` по mTLS.
- `GET /internal/v1/users/{user_id}/orders/{id}` с service/admin authorization.
- `POST /webhooks/yookassa`.
- Refund endpoints только admin/service authorization + audit.

### Subscription

- `GET /v1/users/{user_id}/subscription` — internal ownership-aware query.
- Manual grant/revoke — только admin с reason и audit.

### Access

- `GET /s/{token}` — публичный bearer endpoint.
- `POST /internal/v1/subscriptions/{id}/subscription-url/issue` — одноразовая выдача URL после `access.ready.v1`.
- `POST /internal/v1/subscriptions/{id}/subscription-url/rotate`.
- `GET /internal/v1/subscriptions/{id}/access` — token plaintext не возвращать после момента выдачи без отдельного решения; возможна только ротация.
- `GET /internal/v1/credentials/{credential_id}/provisioning-material` — только для `provisioning-service` по mTLS; возвращает минимальный credential material без REALITY private key.

### Provisioning/node-agent

- `PUT /agent/v1/credentials/{credential_id}` с operation ID/revision.
- `DELETE /agent/v1/credentials/{credential_id}` идемпотентен.
- `GET /agent/v1/health`, `GET /agent/v1/status`.
- Agent принимает только allowlisted fields и никогда не выполняет переданную shell-команду.

### Общие требования API

- стандартный error envelope: `code`, safe `message`, `request_id`, optional field violations;
- публичные ошибки не содержат stack trace, SQL/provider details и PII;
- request/correlation ID генерируется/валидируется, но клиентский произвольный ID не доверяется без ограничений;
- pagination с bounded limit;
- `Idempotency-Key` имеет формат/длину/TTL и привязан к subject + operation + request hash; тот же ключ с другим payload дает conflict;
- timeouts и max body sizes задаются отдельно по endpoint;
- OpenAPI lint и breaking-change check в CI.

---

## 11. Безопасность и privacy

Перед production обязателен `docs/threat-model.md` по STRIDE или аналогичной методике.

### 11.1 Главные активы

- YooKassa credentials и webhook trust chain;
- Telegram bot token;
- subscription bearer tokens;
- Xray client IDs и REALITY private keys;
- node-agent client/server certificates;
- PII и consent records;
- admin credentials;
- ability to provision/revoke access.

### 11.2 Обязательные меры

- TLS везде; mTLS между control plane и node agents.
- Management plane не доступен из публичной сети; firewall default deny.
- Least-privilege DB users, Kafka ACLs, separate credentials per service.
- Secrets не входят в image, Git, Compose defaults или sample values. `.env.example` содержит только имена и безопасные placeholders.
- Production secrets через secret manager или Docker secrets; rotation runbook.
- Encryption at rest для особо чувствительных полей с versioned envelope encryption; master key вне БД.
- Passwords, если web-admin появится: Argon2id по актуальным параметрам, MFA/WebAuthn для администраторов.
- Admin API отделен, RBAC deny-by-default, каждое действие в append-only audit log.
- Rate limits: Telegram update, order/payment create, public subscription URL, admin login, node agent.
- SSRF protection: никакие пользовательские URL не запрашиваются server-side без allowlist.
- Safe HTTP defaults: ReadHeaderTimeout, ReadTimeout/idle strategy, max headers/body, graceful shutdown.
- Outbound egress allowlist для payment adapter и Telegram adapter по возможности.
- Dependency/image pinning по digest для production; non-root, read-only filesystem, dropped Linux capabilities, seccomp where possible.
- Xray и node-agent под отдельными непривилегированными users; privilege separation.
- Backup encrypted, restore регулярно тестируется.
- Не собирать browsing logs. Диагностические IP-адреса и usage aggregates имеют минимальный retention и документированную цель.
- Реализовать privacy export/delete workflow с учетом обязательного хранения финансовых записей.
- Abuse contact, acceptable-use policy, incident response и процесс lawful request должны быть определены до запуска.

### 11.3 Logging policy

Разрешенные поля: timestamp, level, service, version, environment, request_id, correlation_id, operation/event ID, safe entity ID, duration, result code.

Запрещенные поля: full Telegram update/message, username как primary identifier, full webhook, authorization headers, cookies, credentials, confirmation URL query, subscription path/token, VLESS URI/UUID, Xray/REALITY private material, raw SQL arguments с PII, packet/DNS history.

Добавить автоматические tests на redaction и секрет-сканирование логов.

### 11.4 Юридические условия

До реальных продаж требуется профессиональная проверка применимых законов по месту регистрации бизнеса, пользователей и серверов: правила предоставления VPN/прокси, KYC/налоги, чеки и 54-ФЗ при работе в РФ, GDPR/ePrivacy при работе с пользователями ЕС, consumer protection, refund policy, data retention, условия Telegram/YooKassa/VPS-провайдеров. Код не должен маскировать юрисдикционные риски конфигурационным флагом.

---

## 12. Надежность, consistency и отказоустойчивость

- Delivery semantics: at-least-once; exactly-once достигается на уровне бизнес-эффекта через unique constraints, inbox/outbox и state machine.
- Каждый consumer имеет bounded retries, exponential backoff+jitter, DLQ и replay command.
- Никаких бесконечных goroutine или ticker без cancellation.
- Leader election/locking для schedulers через PostgreSQL advisory lock или single-consumer partition; выбрать и описать.
- Readiness проверяет способность обслуживать запрос, но не делает весь сервис unavailable из-за необязательной зависимости.
- Liveness не зависит от Kafka/Postgres transient outage и не вызывает restart loop.
- Graceful shutdown прекращает прием, завершает in-flight до deadline, flushes zap и корректно останавливает consumers.
- Reconciliation jobs для payments, subscription state, access vs allocations и node desired vs actual state.
- Clock skew не должен ломать expiry; системное время узлов синхронизировано, authoritative transition выполняется control plane.
- Capacity policy не overbook’ит узлы; placement учитывает region, health, current load и reserve.
- Agent rollback: invalid Xray config никогда не заменяет last-known-good; после ошибки агент сообщает redacted diagnostic.
- RPO/RTO сначала документировать как цели, затем проверить restore drill.

### Начальные SLO для тестового production

- Control API availability: 99.9% в месяц.
- Subscription endpoint: 99.95% в месяц.
- p95 внутренних простых HTTP-запросов: < 300 ms без внешнего провайдера.
- p95 выдачи subscription document: < 200 ms при теплой БД.
- 99% успешных платежных событий приводят к началу provisioning < 60 секунд.
- Alert при outbox oldest age > 30 секунд, consumer lag, webhook failures, node heartbeat loss, provisioning terminal failure, low capacity.

SLO — начальные допущения, их нужно пересмотреть по метрикам и стоимости.

---

## 13. Docker, Compose и deployment

### Dockerfile каждого Go-сервиса

- multi-stage build;
- reproducible/pinned builder и minimal runtime image;
- `CGO_ENABLED=0`, если зависимости позволяют;
- build args с version/commit/date, доступные через `/version` или metric;
- runtime non-root, read-only root filesystem, writable tmpfs только при необходимости;
- один process на container;
- healthcheck, корректные signals;
- никакого shell/package manager в final image, если не нужен;
- SBOM и image vulnerability scan в CI.

### Compose profiles

- `core`: Postgres, Kafka (KRaft), Redis, migrations, edge.
- `app`: все control-plane сервисы.
- `vpn`: локальный Xray + node-agent для e2e.
- `obs`: Prometheus, Grafana, Tempo, Loki/collector.
- `tools`: Kafka UI, pgAdmin — только local profile, не production.

Compose обязан иметь healthchecks, named volumes, internal networks, resource limits where supported и init/migration jobs. `depends_on` не заменяет application retry.

### Production deployment первой версии

- Control plane: отдельный небольшой кластер/VMs, reverse proxy, managed или HA PostgreSQL, Kafka с минимум тремя brokers только когда оправдано; для малого production допустим надежный managed Kafka.
- VPN nodes: Ansible для ОС hardening, firewall, WireGuard management network, Xray, node-agent, systemd units и ротации сертификатов.
- Нельзя копировать production private keys через GitHub Actions logs/artifacts.
- Разделить public subscription hostname, bot webhook hostname и admin hostname.

---

## 14. Observability

### Метрики

- RED для HTTP: rate, errors, duration.
- Kafka: produced/consumed, lag, retries, DLQ, handler duration.
- DB: pool acquired/idle, acquire wait, query duration по operation name, transaction errors.
- Outbox/inbox: pending count, oldest age, processing failures.
- Billing: payments by state/provider, webhook lag, reconciliation mismatch; без user/payment IDs в labels.
- Subscription: active/expired transitions, scheduler lag.
- Provisioning: operation duration/status, node heartbeat/load/capacity/config revision.
- Subscription endpoint: status class/latency; token и user ID запрещены в labels.

### Tracing

W3C trace context для HTTP; correlation через Kafka headers. Не записывать event body и URL path с bearer token. Sampling настраивается; security/admin операции имеют audit record, но не обязательно полный trace payload.

### Alerts/runbooks

Каждый actionable alert ссылается на runbook: payment webhook failures, Kafka lag, outbox stuck, DB pool exhaustion, expired certificate, node down, capacity threshold, Xray reload failure, backup/restore failure.

---

## 15. Тестовая стратегия

### Unit tests

- state machines order/payment/subscription/access;
- money and duration boundaries;
- renewal across expired/active states;
- idempotency key semantics;
- event schema/envelope validation;
- token generation/hash/rotation/redaction;
- placement policy;
- Xray/Happ config rendering golden tests;
- retry classification.

### Integration tests

- pgx repositories против real PostgreSQL;
- migrations up/down policy или clean apply from zero;
- outbox concurrent workers;
- Kafka producer/consumer + inbox dedupe;
- Redis TTL/rate limit;
- YooKassa fake server: timeout, 5xx, ambiguous result, duplicate/out-of-order webhook, amount mismatch;
- node-agent: desired state, invalid config rollback, repeated operations.

### Contract tests

- OpenAPI request/response conformance;
- backward compatibility published events;
- consumer-driven contracts для важных internal HTTP calls;
- Happ subscription response against official examples.

### E2E

1. Запустить Compose включая Xray/node-agent.
2. Зарегистрировать synthetic Telegram user через fake Bot API/update fixture.
3. Создать order/payment через fake YooKassa.
4. Отправить дубликаты webhook в разных порядках.
5. Убедиться, что создан один subscription period и один credential.
6. Получить subscription URL, импортировать/распарсить VLESS config.
7. Проверить соединение через локальный Xray test endpoint.
8. Сдвинуть test clock/вызвать expiry, убедиться в revoke и недоступности.

### Security tests

- authz matrix и IDOR;
- malformed/oversized requests;
- rate limit;
- SQL injection regression;
- secret/log redaction;
- replay/dedup webhook/event;
- mTLS rejection;
- compromised/expired token;
- container/IaC/dependency scans.

Минимальный CI quality gate: format, vet, lint, unit, race для подходящих пакетов, integration, contract, `govulncheck`, secret scan, image build/scan, migration-from-zero, OpenAPI/AsyncAPI lint.

---

## 16. План реализации для Codex

### Этап 0 — Discovery и ADR, без бизнес-кода

Результаты:

- уточняющие вопросы владельцу продукта;
- `docs/architecture.md`, context/container diagrams;
- `docs/threat-model.md` initial;
- ADR: monorepo, sync/async, database isolation, Kafka client, migrations, money, token storage, Xray management, mTLS, payment verification;
- OpenAPI/AsyncAPI skeleton;
- `AGENTS.md`, `PLANS.md`, Definition of Done;
- risk register и product decision log.

Критерий: все неизвестные, влияющие на деньги/безопасность/API, явно перечислены; код не начат.

### Этап 1 — Repository/platform foundation

- root `go.mod` and service layout decision;
- shared technical platform packages без общего domain;
- config validation, zap, HTTP lifecycle, health/version, request ID;
- Postgres/Kafka/Redis Compose;
- Make targets, lint, CI, migration runner;
- один template service и тесты, затем удалить бессмысленный scaffold duplication.

Критерий: clean clone поднимается одной документированной командой, checks проходят.

### Этап 2 — Identity + Telegram onboarding

- identity schema/service;
- Telegram webhook adapter, dedupe, FSM в Redis;
- `/start`, consent/version, menu;
- contract/integration/e2e tests с fake Telegram.

### Этап 3 — Catalog + Billing + YooKassa sandbox

- immutable plans/prices/order snapshot;
- provider-neutral payment port и YooKassa adapter;
- idempotent create payment, webhook inbox, verification, reconciliation;
- receipts/tax fields только после продуктово-юридического решения;
- fake provider и sandbox test suite.

Критерий: duplicate clicks/webhooks/ambiguous provider response не дают двойной платежный эффект.

### Этап 4 — Subscription lifecycle

- payment event consumer/inbox;
- activation/extension/expiry/revoke state machine;
- scheduler/reconciler;
- notification events;
- time-boundary and concurrency tests.

### Этап 5 — Access + Happ subscription delivery

- credential/token model;
- secure subscription endpoint;
- VLESS + REALITY URI rendering;
- Happ headers/profile/expiry metadata;
- rotation/revocation, no-store/redacted logging;
- compatibility/golden/security tests.

### Этап 6 — Provisioning control plane + node-agent

- node registry/capacity/allocation;
- mTLS identities;
- idempotent desired revision protocol;
- Xray validate/atomic apply/reload/rollback;
- reconcile and failure/DLQ flow;
- local real-Xray e2e.

Запрещено подключать production VPS до прохождения threat review и e2e revoke.

### Этап 7 — Notifications и admin operations

- event-driven Telegram notifications;
- durable delivery/retry;
- minimal admin API/CLI с RBAC и audit;
- support-safe views без credential leakage.

### Этап 8 — Observability, hardening, deployment

- dashboards, alerts, runbooks;
- Ansible node hardening и systemd;
- backup/restore drill;
- load/soak/chaos scenarios;
- secret rotation test;
- privacy retention jobs;
- release images/SBOM/signing.

### Этап 9 — Production readiness review

- полный architecture/security/code review;
- dependency licenses;
- legal/payment/VPS terms checklist;
- incident game day: provider timeout, Kafka outage, DB failover, lost node, leaked token, expired cert;
- go/no-go checklist и rollback plan.

---

## 17. Инструкции для `AGENTS.md`

Codex должен создать короткий `AGENTS.md`, а детали оставить в этом документе. Минимальное содержание:

```markdown
# Repository rules

- Read `docs/CODEX_VPN_PLATFORM_SPEC.md` before architectural or security-sensitive work.
- Work on one approved milestone at a time; plan before editing.
- Preserve service ownership: no cross-service DB access and no shared domain models.
- HTTP uses net/http; SQL uses pgx; logs use zap; async integration uses Kafka outbox/inbox.
- Never log or commit secrets, subscription URLs/tokens, VPN credentials, payment credentials or raw provider/Telegram payloads.
- Every externally retried operation must be idempotent and tested for duplicate/out-of-order delivery.
- Update OpenAPI/AsyncAPI, migrations, tests, ADR/runbook and README with behavior changes.
- Run `make verify` before declaring work complete and report any skipped check.
- Do not change public contracts, event semantics, payment rules, security boundaries or production infrastructure without explicit approval.
- Prefer small reviewable diffs. Review the final diff for security, concurrency, SQL transaction and compatibility issues.
```

---

## 18. Definition of Done для каждой задачи

Задача завершена только если:

- поведение и границы согласованы с текущим этапом;
- бизнес-инварианты выражены кодом и тестами;
- happy path, ошибки, повтор, concurrency и cancellation рассмотрены;
- schema/API/event docs обновлены;
- миграции применяются с нуля и имеют rollout strategy;
- logs/metrics/traces не раскрывают секреты/PII;
- unit + relevant integration/contract/e2e tests проходят;
- formatter/vet/lint/race/vulnerability/secret scans проходят либо ограничение явно задокументировано;
- Docker image собирается и сервис работает non-root;
- README/runbook содержит запуск, диагностику и rollback;
- diff проверен на cross-service coupling, dead code и случайные изменения;
- нет TODO, скрывающего security/correctness blocker;
- критерии приемки подтверждены конкретными командами и результатами.

---

## 19. Вопросы владельцу продукта до реализации

Codex должен задать и зафиксировать ответы минимум на эти вопросы:

1. В какой стране зарегистрирован продавец и где будут покупатели?
2. Нужны разовые предоплаченные периоды или автосписания?
3. Какие тарифы: длительность, трафик, число устройств, регионы, grace period?
4. Что происходит при покупке поверх активной подписки?
5. Как возврат влияет на уже выданный доступ?
6. Сколько узлов должно назначаться одной подписке: все регионы или выбранный?
7. Нужен лимит устройств и готов ли продукт использовать HWID Happ с учетом privacy?
8. Нужна ли статистика трафика и в каком минимальном виде?
9. Какой домен, DNS/TLS-провайдер и VPS-провайдеры планируются?
10. Требуются ли чеки YooKassa/54-ФЗ, НДС и какие данные покупателя разрешено запрашивать?
11. Кто имеет admin-доступ и нужен ли web-admin или достаточно защищенного CLI на первом этапе?
12. Каковы желаемые SLO, бюджет и допустимый oversubscription?
13. Какой retention финансовых, audit и диагностических данных?
14. Нужна ли геоблокировка/санкционные ограничения или отказ в обслуживании отдельных регионов?
15. Какой support/abuse процесс и сроки реакции?

Пока ответов нет, использовать безопасные допущения: prepaid без автосписаний, один пользователь — одна продлеваемая подписка, VLESS + REALITY, минимальная телеметрия, один выбранный регион плюс failover, ручное подтверждение refunds оператором. Допущения не выдавать за окончательные требования.

---

## 20. Финальные критерии приемки всей платформы

- Новый пользователь проходит путь Telegram → тариф → YooKassa sandbox → активная подписка → Happ URL → тестовое VPN-соединение.
- Повтор любого Telegram update, HTTP request с тем же idempotency key, YooKassa webhook или Kafka event не создает двойного эффекта.
- Несовпадение суммы/валюты/status у провайдера никогда не активирует доступ.
- Утечка subscription token устраняется ротацией без смены пользовательской учетной записи.
- Истечение, блокировка и refund policy приводят к согласованному отзыву со всех узлов.
- Недоступность Kafka после успешного DB commit не теряет событие; outbox доставляет его после восстановления.
- Недоступность control plane не ломает уже примененный last-known-good data plane.
- Поврежденная Xray-конфигурация не заменяет рабочую.
- В репозитории, логах, traces и metrics отсутствуют secrets и bearer URL.
- Базы сервисов изолированы, cross-service SQL отсутствует.
- Clean clone поднимается через Compose; migrations и seed test data документированы.
- CI воспроизводимо проходит все обязательные quality gates.
- Backup восстановлен в отдельном окружении; node loss и leaked-token runbooks проверены.
- Architecture, threat model, API/events, operations и portfolio README соответствуют коду.

---

## 21. Официальные источники, которые Codex обязан перепроверять

Документация меняется; перед реализацией интеграции Codex должен открыть актуальную официальную страницу, а не полагаться только на этот файл.

- Codex best practices: https://developers.openai.com/codex/learn/best-practices
- Codex `AGENTS.md`: https://developers.openai.com/codex/guides/agents-md
- YooKassa API interaction: https://yookassa.ru/developers/using-api/interaction-format
- YooKassa payment lifecycle: https://yookassa.ru/developers/payment-acceptance/getting-started/payment-process
- YooKassa webhooks: https://yookassa.ru/developers/using-api/webhooks
- Happ developer documentation: https://www.happ.su/main/dev-docs
- Happ subscription/app parameters: https://www.happ.su/main/dev-docs/app-management
- Happ supported link examples: https://www.happ.su/main/dev-docs/examples-of-links-and-parameters
- Xray-core documentation/repository: использовать только официальный проект XTLS/Xray-core и закрепленную проверенную версию.

---

## 22. Итоговое указание Codex

Качество этого проекта определяется не количеством сервисов и технологий, а корректностью границ, платежной идемпотентностью, безопасным lifecycle credential, воспроизводимостью инфраструктуры и проверяемостью отказов. Не имитируй production-quality пустыми интерфейсами, boilerplate или диаграммами. Каждый добавленный механизм должен иметь реальный use case, тест, метрику и понятную эксплуатационную модель.

Начни с Этапа 0. До получения ответов на вопросы, влияющие на деньги, право, privacy и способ отзыва доступа, не реализуй необратимые контракты.
