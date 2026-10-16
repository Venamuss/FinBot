# FinBot

Текущий инкремент: создание и просмотр расходов через Go REST API с PostgreSQL. Архитектура и дальнейшие этапы: [docs/architecture.md](docs/architecture.md), [docs/tasks.md](docs/tasks.md). Исходное ТЗ: [docs/test-task-expenses.md](docs/test-task-expenses.md); точный контракт: [docs/api.md](docs/api.md).

Реализованы `POST /api/expenses`, `GET /api/expenses/{id}` и `GET /api/expenses`. PATCH, DELETE, `/api/members`, Telegram, LLM и React пока отсутствуют. REST не требует авторизации и позволяет работать со всеми владельцами; Telegram allowlist к нему не применяется.

## Запуск

Требуются Docker Engine и Docker Compose v2+ с доступом к реестру образов, Go proxy и Alpine packages.

```sh
cp .env.example .env
# Задайте в .env собственный POSTGRES_PASSWORD, затем:
docker compose up -d --build --wait
curl http://127.0.0.1:8080/api/expenses
```

Пароль для Compose должен состоять из URL-safe символов (буквы, цифры, `-`, `_`): он включается в URI подключения. `.env` не коммитится. Go-образ собирается без frontend. Миграции встроены в Go-бинарник и выполняются через Goose до открытия HTTP-порта; каждая миграция и запись её версии применяются транзакционно. Повторный запуск не применяет завершённые миграции заново, одновременные запуски защищены advisory lock.

SQL-файлы в `backend/migrations/` используют числовые версии и секции `-- +goose Up` / `-- +goose Down`. Приложение автоматически выполняет только Up; Down первой миграции удаляет таблицу расходов. История Goose хранится в `goose_db_version`. При обновлении с прежнего runner начальная миграция сохраняет существующие `expenses`, индекс и данные через `IF NOT EXISTS`; прежняя `schema_migrations` остаётся нетронутой и больше не используется. Такой переход рассчитан на исходную схему FinBot, а не на вручную изменённые таблицы.

По умолчанию HTTP доступен только на `127.0.0.1:8080`. `HTTP_PORT` меняет порт хоста; `HTTP_BIND_IP` меняет интерфейс публикации. PostgreSQL не имеет опубликованного порта; данные находятся в именованном томе `postgres_data`. `docker compose down` сохраняет том; `docker compose down -v` удаляет данные. Изменение пароля в `.env` само по себе не меняет пароль уже созданной БД.

```sh
curl -i http://127.0.0.1:8080/api/expenses \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"123456789","amount":"350.5","category":"еда","spent_at":"2026-10-16"}'
curl 'http://127.0.0.1:8080/api/expenses?user_id=123456789&sort=amount&order=asc&limit=10'
```

Суммы и ID передаются строками. Ответ нормализует сумму до двух знаков. Отсутствующий комментарий возвращается как `null`, пустая строка сохраняется. Даты календарные; временные метки возвращаются в UTC. Изменяющий запрос с `Origin` принимается только при совпадении со схемой и Host запроса. Reverse proxy должен сохранять исходный Host; доверие к `X-Forwarded-*` пока не настраивается.

## Разработка и проверки

Go 1.27.1 или новее; прямые зависимости — `pgx/v5` для PostgreSQL и `goose/v3` для миграций. Goose использует адаптер `database/sql` поверх существующего `pgxpool`, отдельный драйвер БД не добавляется. Из `backend/`:

```sh
gofmt -w cmd internal migrations
go test ./...
go vet ./...
DATABASE_URL='postgres://user:password@host:5432/finbot?sslmode=disable' go run ./cmd/finbot
```

`DATABASE_URL` обязателен. `HTTP_ADDR` по умолчанию `127.0.0.1:8080`; Compose задаёт `:8080` внутри контейнера. Запуск ожидает БД и применяет миграции в пределах 30 секунд; при неуспехе процесс завершается до приёма запросов. SIGTERM/SIGINT останавливают HTTP с ожиданием активных запросов до 10 секунд, затем закрывается пул БД. Настройки Telegram и LLM будут добавлены в соответствующих этапах.

Без `TEST_DATABASE_URL` тесты repository явно пропускаются. Для реальной интеграционной проверки задайте адрес тестовой PostgreSQL и выполните из `backend/`:

```sh
TEST_DATABASE_URL='postgres://user:password@host:5432/testdb?sslmode=disable' \
  go test ./internal/postgres -run Integration -count=1 -v
```

Тестовый пользователь должен иметь право создавать схемы. Тесты создают отдельные случайные схемы и удаляют только их. Проверяются чистое и повторное применение миграций, одновременный запуск Goose, переход с прежней схемы с сохранением расходов и Down/Up в изолированной тестовой схеме. Существующие таблицы не очищаются. Адрес БД Compose можно использовать из Go-контейнера в той же сети без публикации PostgreSQL. Например, из корня проекта для стандартного имени Compose-проекта `finbot` (замените `test-password` на пароль тестовой установки):

```sh
docker run --rm --network finbot_default \
  -v "$PWD/backend:/src" -w /src \
  -e 'TEST_DATABASE_URL=postgres://finbot:test-password@db:5432/finbot?sslmode=disable' \
  golang:1.27.1-alpine go test ./internal/postgres -run Integration -count=1 -v
```

HTTP smoke использует Python 3.9+ без дополнительных пакетов. Запускайте его только на тестовой установке: он создаёт четыре синтетических расхода и оставляет их для проверки сохранности. Из корня репозитория:

```sh
python3 scripts/smoke.py --state-file /tmp/finbot-smoke.json
docker compose restart
python3 scripts/smoke.py --state-file /tmp/finbot-smoke.json --verify
docker compose down
docker compose up -d --wait
python3 scripts/smoke.py --state-file /tmp/finbot-smoke.json --verify
```

Для другого HTTP-порта передайте `--url http://127.0.0.1:PORT`. Скрипт проверяет создание, чтение, комбинируемые фильтры, сортировку, страницы, форматы и ошибки. Повторный `--verify` проверяет записи после перезапуска и пересоздания контейнеров с сохранённым томом. Результаты проверок текущего инкремента и ограничения среды записаны в [docs/tasks.md](docs/tasks.md).
