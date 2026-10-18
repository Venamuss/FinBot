# FinBot

[Русский](#russian) · [English](#english)

<a id="russian"></a>

FinBot — приложение для учёта семейных расходов, которое объединяет ИИ-помощника в Telegram и панель управления на React. Члены семьи ведут свои расходы на естественном языке, а панель позволяет управлять расходами всей семьи через общий бэкенд на Go.

![Go 1.27.1](https://img.shields.io/badge/Go-1.27.1-00ADD8?logo=go&logoColor=white)
[![CI настроен](https://img.shields.io/badge/GitHub_Actions-CI_configured-555?logo=githubactions&logoColor=white)](https://github.com/Venamuss/FinBot/actions/workflows/ci.yml)

[Исходное задание](docs/test-task-expenses.md) · [Требования](docs/requirements.md) · [Справочник API](docs/api.md) · [Отчёт о проверках и готовности к сдаче](docs/submission-readiness.md)

## Возможности

- **Учёт расходов через Telegram:** создание, просмотр, подсчёт сумм, изменение и удаление расходов обычным текстом на русском языке, включая относительные даты и ссылки на предыдущие траты.
- **Изоляция владельцев:** разрешённым пользователям Telegram доступны только собственные расходы; неоднозначные изменения требуют уточнения.
- **Управление расходами семьи:** создание, изменение, удаление, фильтры по участнику, категории и дате, сортировка и страницы в русскоязычной панели на React.
- **Имена участников:** необязательные редактируемые имена рядом с неизменяемыми Telegram ID.
- **Точные суммы:** десятичные строки и целое число сотых с хранением в PostgreSQL.
- **Настраиваемый ИИ:** один OpenAI-совместимый API с вызовом инструментов; провайдер по умолчанию — OpenRouter.
- **Запуск через Docker:** nginx, Go, PostgreSQL, проверки готовности, автоматические миграции и постоянное хранение данных.

Для воспроизводимой демонстрации пройдите [сценарий ручной приёмки](docs/submission-readiness.md#f-manual-acceptance-test). Скриншоты не включены в репозиторий; [инструкция по съёмке](docs/screenshots/README.md) описывает реальные снимки приложения на компьютере и телефоне с синтетическими данными.

## Технологии

| Область | Реализация |
|---|---|
| Бэкенд | Go 1.27.1, `net/http`, pgx v5, миграции Goose |
| Фронтенд | React 19, TypeScript 5.9, Vite 7, стандартный fetch и CSS |
| База данных | PostgreSQL 17; обычные параметризованные SQL-запросы |
| ИИ / Telegram | OpenAI-совместимый Chat Completions с вызовом инструментов; long polling Telegram Bot API |
| Развёртывание | Docker Compose; nginx раздаёт React и проксирует REST |
| Тесты / CI | Тесты Go и детектор гонок, интеграционные тесты PostgreSQL, Vitest, Testing Library, jsdom; GitHub Actions |

Версии указаны в [go.mod](backend/go.mod), [package.json](frontend/package.json) и [Compose](compose.yaml). В корне проекта нет файла лицензии; встроенный шрифт распространяется по отдельной [лицензии OFL](frontend/public/fonts/OFL.txt).

## Архитектура

```mermaid
flowchart LR
    TU[Telegram user] --> TB[Telegram bot]
    TB --> CO[Conversation orchestration]
    CO -->|validated operations| ES[Expense services]
    CO -->|tool definitions and user context| LLM[Configured LLM API / OpenRouter]
    LLM -->|proposed tool call| CO
    WA[React web admin] --> NG[nginx]
    NG --> API[Go REST API]
    API --> ES
    ES --> PG[(PostgreSQL)]
    CO -->|transactional update and reply storage| PG
```

REST и Telegram используют общие правила проверки расходов и хранения данных внутри модульного монолита на Go. При чтении и изменении расходов бот ограничивает SQL-запросы идентификатором владельца, полученным от доверенного отправителя. LLM предлагает действие; Go проверяет его аргументы и права доступа, а модель не имеет SQL- или административных инструментов. PostgreSQL хранит расходы, необязательные имена участников, историю миграций и зафиксированные результаты обработки Telegram. Подробнее — в [описании архитектуры](docs/architecture.md).

## Быстрый старт

Нужны Docker Engine и Docker Compose v2+ с доступом к реестрам образов, модулей Go, npm и пакетов Alpine. Для запуска через Docker устанавливать Go и Node на хосте не требуется.

```sh
git clone https://github.com/Venamuss/FinBot.git
cd FinBot
cp .env.example .env
```

Перед запуском отредактируйте `.env`:

1. Замените `POSTGRES_PASSWORD` случайным локальным паролем, содержащим только буквы, цифры, `-` и `_`. Compose подставляет его в URI подключения к базе.
2. Для Telegram задайте `TELEGRAM_BOT_TOKEN`, полученный у BotFather, и перечислите собственные Telegram ID через запятую в `TELEGRAM_ALLOWED_USER_IDS`.
3. Для OpenRouter задайте `LLM_API_KEY`. В шаблоне явно выбрана модель `openai/gpt-4.1-mini`; используемая модель должна поддерживать вызов инструментов. Провайдер может взимать плату за запросы.

Панель можно сразу запустить с пустыми токеном бота и ключом: опрос Telegram отключён, запросы к модели не выполняются. Пустой список разрешённых ID запрещает доступ к боту всем пользователям.

```sh
docker compose up -d --build --wait
docker compose ps
curl --fail http://127.0.0.1:8080/api/expenses
```

Откройте **[http://127.0.0.1:8080](http://127.0.0.1:8080)**. Все три сервиса (`db`, `app`, `frontend`) должны иметь статус healthy; на новой базе API возвращает `items: []` и `total: 0`. Миграции выполняются автоматически до готовности приложения. Доступность Telegram и LLM не влияет на готовность панели.

```sh
docker compose logs --tail=50 app
docker compose down
```

`down` сохраняет том базы данных; **`down --volumes` удаляет его данные**. Не удаляйте том установки, данные которой нужно сохранить. Изменение `POSTGRES_PASSWORD` не меняет пароль уже инициализированной базы. После редактирования `.env` выполните `docker compose up -d`, чтобы пересоздать затронутые сервисы с новой конфигурацией; один лишь `restart` не перечитывает переменные окружения.

Порт публикует только nginx; по умолчанию он привязан к loopback-интерфейсу. Если порт 8080 занят, измените `HTTP_PORT`. Сохраняйте `HTTP_BIND_IP=127.0.0.1`, если не планируете намеренно открыть доступ из доверенной сети.

## Переменные окружения

[.env.example](.env.example) — шаблон для Compose. Процесс Go читает окружение один раз и сам не загружает `.env`. Некорректная конфигурация останавливает запуск; её значения в логи не попадают.

### База данных и HTTP

| Переменная | Обязательность / значение по умолчанию | Назначение |
|---|---|---|
| `POSTGRES_PASSWORD` | Обязательна для Compose; замените значение шаблона | Инициализация базы и формирование DSN приложения |
| `DATABASE_URL` | Обязательна при прямом запуске Go; в Compose задаётся автоматически | DSN PostgreSQL; при самостоятельном составлении URI кодируйте учётные данные для URL |
| `HTTP_ADDR` | Go: `127.0.0.1:8080`; Compose: `:8080` | Адрес HTTP-сервера бэкенда; Compose фиксирует внутренний порт для nginx |
| `HTTP_BIND_IP` | `127.0.0.1` | Интерфейс хоста для публикации порта в Compose |
| `HTTP_PORT` | `8080` | Порт хоста в Compose |
| `TIME_ZONE` | `Europe/Moscow` | Часовой пояс IANA для относительных дат в сообщениях боту |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` без учёта регистра |
| `HTTP_REQUEST_TIMEOUT` | `5s` | Дедлайн REST-операции; положительный и меньше фиксированного таймаута записи HTTP в 15 секунд |
| `STARTUP_TIMEOUT` | `30s` | Общий дедлайн ожидания базы и выполнения миграций |

В стандартном Compose база и пользователь называются `finbot`; Go и PostgreSQL не публикуют порты на хосте. Дополнительные параметры соединения и пула задаются в `DATABASE_URL` при прямом запуске либо через переопределение Compose.

### Telegram и LLM

| Переменная | Обязательность / значение по умолчанию | Назначение |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | Необязательна, пустая | Пустое значение отключает опрос; для работы с Telegram нужен токен |
| `TELEGRAM_ALLOWED_USER_IDS` | Необязательна, пустая | Положительные ID через запятую; пустое значение запрещает доступ всем; некорректные записи останавливают запуск |
| `TELEGRAM_POLL_TIMEOUT` | `25s` | Целое число секунд от 1s до 50s |
| `TELEGRAM_HTTP_TIMEOUT` | `40s` | HTTP-таймаут одной попытки; должен превышать таймаут опроса |
| `TELEGRAM_RETRY_DELAY` | `1s` | Базовая задержка повторных API-запросов; не более трёх попыток |
| `TELEGRAM_POLL_RETRY_DELAY` | `5s` | Задержка перед повтором неудачного опроса или неподтверждённого обновления |
| `LLM_BASE_URL` | `https://openrouter.ai/api/v1` | Абсолютный HTTP(S)-префикс API; адаптер добавляет `/chat/completions` |
| `LLM_MODEL` | Go/Compose: `google/gemma-4-31b-it:free`; шаблон: `openai/gpt-4.1-mini` | Явное значение шаблона заменяет значение по умолчанию при копировании в `.env` |
| `LLM_API_KEY` | Необязательна, пустая | Нужна для OpenRouter; для локального API можно не задавать |
| `LLM_TIMEOUT` | `30s` | Положительная длительность в формате Go для одного запроса к модели |

Все длительности должны быть положительными. ID в списке доступа указываются без знака и ведущих нулей; пробелы вокруг записей и повторы допустимы. Приложение использует один настроенный API без автоматического перехода в облако или повтора запроса к модели. Локальный API должен быть доступен **из контейнера приложения**; `localhost` внутри контейнера указывает на сам контейнер.

### Необязательный прокси и разработка фронтенда

| Переменная | Значение по умолчанию | Назначение |
|---|---|---|
| `HTTP_PROXY`, `HTTPS_PROXY` | Пустые, если не заданы | Стандартные настройки исходящего прокси Go, передаваемые через Compose в `app` |
| `NO_PROXY` | Compose: `localhost,127.0.0.1,db` | Хосты, для которых прокси не используется |
| `FINBOT_DEV_API_URL` | `http://127.0.0.1:8080` | Адрес назначения прокси только для сервера разработки Vite |

Прокси в основном нужен в сетях с ограниченным доступом. При прямом подключении оставьте эти переменные незаданными; VPN и адрес шлюза Docker не требуются. При необходимости добавьте имя хоста локальной модели в `NO_PROXY`. Учётные данные бэкенда нельзя помещать в переменные фронтенда `VITE_*`.

## Использование

Отправляйте боту личные текстовые сообщения из разрешённого аккаунта:

| Действие | Пример |
|---|---|
| Создать расход | `еда 500`, `кофе 350` |
| Указать относительную дату | `такси 1200 вчера` |
| Посмотреть расходы за всё время | `мои расходы` |
| Посмотреть расходы за сегодня | `мои расходы за сегодня` |
| Посмотреть последние семь дней | `покажи траты за неделю` |
| Получить сумму расходов на еду за месяц | `сколько я потратил на еду в этом месяце` |
| Исправить выбранную трату | `последнее было не 350, а 450` |
| Удалить расход | `удали последнюю трату`, `удали такси` |

Перед удалением бот показывает выбранный расход и просит ответить `да` или `нет`. Выбор из нескольких вариантов ещё не удаляет запись. Подтверждение действует 30 минут; изменение записи в панели требует нового выбора. `стоп`, `отмена`, `отмени`, `cancel` отменяют ожидающее действие без изменения расходов; повторная отмена и `да` без подтверждения безопасны. Слова отмены внутри описания, например `купил стоп-сигнал за 500`, остаются частью расхода.

При просмотре расходов без указания периода, например «мои расходы», показываются расходы за всё время. При создании расхода и подсчёте суммы без указания даты используется сегодняшний день в `TIME_ZONE` относительно времени исходного сообщения Telegram. «За неделю» включает сегодня и шесть предыдущих дней; «на этой неделе» — период с понедельника. После интерпретации моделью относительные даты вычисляет сервер.

Текущая выбранная в диалоге трата имеет приоритет над последней созданной записью. Если выбранной траты нет, «последняя» определяется по `created_at DESC, id DESC`, независимо от даты покупки. Несовпадение предыдущей суммы или несколько подходящих записей вызывают уточнение: ответьте предложенным номером либо отмените выбор текстом. После изменения через панель устаревшую выбранную трату нужно выбрать заново. Контекст выбора истекает через 30 минут и теряется при перезапуске.

В панели используйте **«Добавить расход»**, действия изменения и удаления в строках, а также фильтры по участнику, категории и датам. Раздел **«Участники семьи»** позволяет менять отображаемые имена; очистка имени возвращает числовой ID. Имена не меняют владельцев расходов или права доступа к боту. Суммы не имеют обозначения валюты и не конвертируются. На узком экране таблица прокручивается горизонтально.

## Обзор API

| Метод | Путь | Назначение |
|---|---|---|
| `GET` | `/api/expenses` | Список с фильтрами, сортировкой и страницами |
| `POST` | `/api/expenses` | Создать расход |
| `GET` | `/api/expenses/{id}` | Получить расход |
| `PATCH` | `/api/expenses/{id}` | Изменить переданные поля |
| `DELETE` | `/api/expenses/{id}` | Удалить; возвращает 204 без тела |
| `GET` | `/api/members` | Владельцы расходов и сохранённые имена участников |
| `PUT` | `/api/members/{user_id}` | Задать или очистить отображаемое имя |

ID и суммы передаются в JSON **строками**; `spent_at` — календарная дата (`YYYY-MM-DD`). Фильтры объединяются через AND; границы дат включаются в период. Форматы ответов, категории, проверки, ограничения и ошибки описаны в [REST-контракте](docs/api.md). `/readyz` — внутренний эндпоинт приложения, недоступный через REST-прокси.

## Разработка и тестирование

Используйте Go 1.27.1 и Node.js 24/npm. Из корня репозитория:

```sh
cd backend
test -z "$(gofmt -l cmd internal migrations)"
go vet ./...
go test ./... -count=1 -timeout=180s
cd ../frontend
npm ci
npm run typecheck
npm test
npm run build
npm run dev
```

Vite работает на `http://127.0.0.1:5173` и проксирует `/api` на порт 8080; бэкендом может служить стандартная установка Compose. Для другого порта задайте `FINBOT_DEV_API_URL` перед `npm run dev` или в `frontend/.env.local`.

### Интеграционные тесты PostgreSQL

Для детектора гонок нужны CGO и компилятор C. Тестовая роль базы данных должна иметь право создавать схемы.

Без `TEST_DATABASE_URL` тесты, зависящие от базы данных, явно **пропускаются**. Используйте одноразовую тестовую базу, а не базу разработки. Из корня репозитория следующий пример публикует тестовый порт только на loopback-интерфейсе и не использует именованный том (`--rm` удаляет одноразовый контейнер и его анонимный том):

```sh
docker run -d --rm --name finbot-test-db \
  -e POSTGRES_USER=finbot_test \
  -e POSTGRES_PASSWORD=disposable-test-password \
  -e POSTGRES_DB=finbot_test \
  -p 127.0.0.1:15432:5432 postgres:17-alpine
until docker exec finbot-test-db pg_isready -U finbot_test -d finbot_test; do sleep 1; done
cd backend
TEST_DATABASE_URL='postgres://finbot_test:disposable-test-password@127.0.0.1:15432/finbot_test?sslmode=disable' \
  go test -race ./... -count=1 -timeout=180s
cd ..
docker stop finbot-test-db
```

Интеграционные тесты создают и удаляют случайные схемы внутри выбранной базы. Они проверяют миграции, CRUD, точные суммы, откат транзакций, конкурентные операции, владение расходами, повторные обновления и цепочку Telegram/LLM с локальными имитациями API. Автоматические тесты не обращаются к реальному Telegram или платной модели. Vitest проверяет формы, безопасность изменений, ошибки API, страницы, имена участников и точность работы с датами и строковыми суммами.

Дополнительные проверки из корня репозитория:

```sh
docker compose config --quiet
cd frontend
TZ=America/Los_Angeles npm test -- src/validation.test.ts
TZ=Pacific/Kiritimati npm test -- src/validation.test.ts
```

[Процесс CI](.github/workflows/ci.yml) проверяет форматирование и vet, полный набор тестов Go с детектором гонок и PostgreSQL 17, тесты и сборку фронтенда с проверкой типов, часовые пояса, оба Docker-образа, запуск Compose и сохранность REST-данных после перезапуска и пересоздания контейнеров. Успешный запуск на GitHub для этой сдачи **не подтверждён**; значок означает только наличие конфигурации. Фактические локальные результаты и оставшиеся ручные проверки указаны в [отчёте QA](docs/submission-readiness.md).

## Безопасность

- Проверка доступа к боту выполняется до обращения к модели, чтения сохранённых ответов и выполнения операций. Владелец определяется исключительно по Telegram `message.from.id`, а не по тексту, пересланным данным отправителя или аргументам модели.
- SQL-условия по владельцу защищают чтение, подсчёт сумм, изменение и удаление через бота. Значения данных параметризованы; выражения сортировки выбираются из фиксированных списков.
- Панель и REST API намеренно работают **без аутентификации**, как допускает задание. Любой клиент с сетевым доступом может управлять всеми расходами независимо от списка доступа к боту. Сохраняйте привязку к loopback-интерфейсу или используйте доверенную локальную сеть.
- Изменяющие запросы браузера проверяют Origin, если он указан; это не аутентификация. Обратный прокси должен сохранять Host. Завершение TLS и доверие к пересылаемым заголовкам требуют отдельной настройки развёртывания.
- Учётные данные передаются через окружение. `.env` исключён из Git. Логи приложения не содержат учётных данных, текстов сообщений и запросов к модели, аргументов инструментов, содержимого расходов и имён участников. Настроенный провайдер модели получает текст сообщения и минимальный контекст текущего пользователя.

## Структура проекта

```text
backend/
  cmd/finbot/             Запуск, проверка готовности, корректная остановка
  internal/
    expense/             Предметные правила и прикладные сервисы
    postgres/            SQL-репозитории и транзакции Telegram
    httpapi/             Административные REST-адаптеры
    telegram/            Клиент Bot API и опрос обновлений
    conversation/        Проверка инструментов, выбор траты, уточнения
    llm/                 OpenAI-совместимый клиент
    config/              Проверка переменных окружения
    observability/       Безопасные структурированные логи
  migrations/            Встроенные миграции Goose
frontend/                Панель React/TypeScript и тесты компонентов
scripts/smoke.py          REST-проверка и проверка сохранности тестовой установки
.github/workflows/       Проверки CI
docs/                    Задание, контракты, архитектура, QA
compose.yaml             Локальное развёртывание из трёх сервисов
```

## Проектные решения

- **Точные денежные суммы:** целое число сотых в Go, десятичные строки в JSON и `NUMERIC(14,2)` в PostgreSQL. Вычисления не используют числа с плавающей точкой; лишние дробные знаки отклоняются без округления. SQL-итоги возвращаются десятичным текстом, в том числе если число сотых превышает диапазон int64.
- **Общие бизнес-правила:** разбор транспортных данных остаётся в HTTP- и Telegram-адаптерах; оба сценария используют сервисы расходов и репозитории PostgreSQL.
- **Атомарная обработка Telegram:** уникальный `update_id`, изменение расхода и ответ фиксируются вместе. При сбое доставки возвращается сохранённый результат без повторного изменения; внешние API вызываются вне транзакций.
- **Безопасный выбор записи:** владелец и `updated_at` проверяются атомарно при выполнении отложенного изменения. Модель предлагает действие, а сервер проверяет права.
- **Небольшое развёртывание:** один процесс Go, одна база PostgreSQL и статический React за nginx. Без таблицы пользователей, брокера сообщений, ORM и отдельного сервиса агента.

## Известные ограничения

- Ручная приёмка Telegram с разрешёнными пользователями A/B и запрещённым C ещё не выполнена. Тесты с имитациями API и проверка панели в реальном браузере прошли, но не подтверждают текущую интерпретацию провайдером или доставку реальным аккаунтам. См. [вывод о готовности к сдаче](docs/submission-readiness.md#g-final-verdict).
- Опрос выполняется последовательно одним процессом; постоянная ошибка доставки ответа может заблокировать очередь. При неоднозначном результате отправки ответ может прийти повторно, но изменение расхода остаётся идемпотентным.
- Контекст выбора траты хранится в памяти. Списки бота содержат первые 20 записей и общее количество; если кандидатов на изменение больше 10, нужно сузить запрос. Поиск по описанию использует буквальную подстроку комментария.
- Для сохранённых ответов Telegram нет очистки по сроку хранения или постоянной очереди доставки. Удаление расхода не удаляет ранее сохранённые ответы.
- Одна семья на установку, личные текстовые сообщения, одно изменение расхода на запрос, без конвертации валют. Страницы со смещением могут сдвигаться при конкурентных изменениях; один ответ использует согласованный снимок данных.
- Проверка реального локального LLM необязательна и не выполнялась. При сбое настроенного провайдера автоматического перехода на другой нет.

---

<a id="english"></a>

# FinBot

FinBot is a family expense management application combining a Telegram AI assistant with a React administration panel. Family members track their own expenses in natural language; the panel manages expenses across the family through a shared Go backend.

![Go 1.27.1](https://img.shields.io/badge/Go-1.27.1-00ADD8?logo=go&logoColor=white)
[![CI configured](https://img.shields.io/badge/GitHub_Actions-CI_configured-555?logo=githubactions&logoColor=white)](https://github.com/Venamuss/FinBot/actions/workflows/ci.yml)

[Original assignment](docs/test-task-expenses.md) · [Requirements](docs/requirements.md) · [API reference](docs/api.md) · [QA and submission report](docs/submission-readiness.md)

## Features

- **Telegram expense tracking:** create, list, total, edit, and delete expenses using Russian text, including relative dates and conversational references.
- **Owner isolation:** allowed Telegram users can access only their own expenses; ambiguous changes require clarification.
- **Family administration:** create, edit, delete, filter by member/category/date, sort, and paginate expenses in a Russian-language React panel.
- **Member display names:** optional editable names alongside immutable Telegram IDs.
- **Exact amounts:** decimal strings and integer hundredths, with PostgreSQL persistence.
- **Configurable AI:** one OpenAI-compatible endpoint with tool calling; OpenRouter is the default provider.
- **Docker startup:** nginx, Go, PostgreSQL, readiness checks, automatic migrations, and persistent storage.

For a reproducible demonstration, follow the [manual acceptance scenario](docs/submission-readiness.md#f-manual-acceptance-test). Screenshots are not bundled; see [capture guidance](docs/screenshots/README.md) for real desktop/mobile captures using synthetic data.

## Tech stack

| Area | Implementation |
|---|---|
| Backend | Go 1.27.1, `net/http`, pgx v5, Goose migrations |
| Frontend | React 19, TypeScript 5.9, Vite 7, native fetch and CSS |
| Database | PostgreSQL 17; plain parameterized SQL |
| AI / Telegram | OpenAI-compatible Chat Completions with function tools; Telegram Bot API long polling |
| Deployment | Docker Compose; nginx serves React and proxies REST |
| Tests / CI | Go tests and race detector, PostgreSQL integration tests, Vitest, Testing Library, jsdom; GitHub Actions |

Versions come from [go.mod](backend/go.mod), [package.json](frontend/package.json), and [Compose](compose.yaml). There is no root project license file; the bundled font has its own [OFL license](frontend/public/fonts/OFL.txt).

## Architecture

```mermaid
flowchart LR
    TU[Telegram user] --> TB[Telegram bot]
    TB --> CO[Conversation orchestration]
    CO -->|validated operations| ES[Expense services]
    CO -->|tool definitions and user context| LLM[Configured LLM API / OpenRouter]
    LLM -->|proposed tool call| CO
    WA[React web admin] --> NG[nginx]
    NG --> API[Go REST API]
    API --> ES
    ES --> PG[(PostgreSQL)]
    CO -->|transactional update and reply storage| PG
```

REST and Telegram share expense validation and persistence inside a modular Go monolith. Bot reads and writes enforce the trusted sender's owner ID in SQL. The LLM proposes an action; Go validates its arguments and permissions, and the model has no SQL or administrative tools. PostgreSQL stores expenses, optional member names, migration history, and committed Telegram results. See [architecture details](docs/architecture.md).

## Quick start

Requires Docker Engine and Docker Compose v2+ with access to the image, Go, npm, and Alpine package registries. Host Go and Node installations are unnecessary for Docker startup.

```sh
git clone https://github.com/Venamuss/FinBot.git
cd FinBot
cp .env.example .env
```

Edit `.env` before starting:

1. Replace `POSTGRES_PASSWORD` with a random local password containing letters, digits, `-`, and `_` only. Compose inserts it into a database URI.
2. For Telegram, set `TELEGRAM_BOT_TOKEN` from BotFather and `TELEGRAM_ALLOWED_USER_IDS` to your own comma-separated Telegram IDs.
3. For OpenRouter, set `LLM_API_KEY`. The template explicitly selects `openai/gpt-4.1-mini`; keep a model supporting tool calling. Requests may incur provider charges.

The panel can run immediately with a blank bot token and key: polling is disabled, and no model request is made. An empty allowlist denies every bot user.

```sh
docker compose up -d --build --wait
docker compose ps
curl --fail http://127.0.0.1:8080/api/expenses
```

Open **[http://127.0.0.1:8080](http://127.0.0.1:8080)**. All three services (`db`, `app`, `frontend`) should be healthy; a fresh API returns `items: []` and `total: 0`. Migrations run automatically before the application becomes ready. Telegram/LLM availability does not determine panel readiness.

```sh
docker compose logs --tail=50 app
docker compose down
```

`down` preserves the database volume; **`down --volumes` deletes its data**. Do not use volume removal on an installation you want to retain. Changing `POSTGRES_PASSWORD` does not update an already initialized database password. After editing `.env`, run `docker compose up -d` to recreate affected services with the new configuration; `restart` alone does not reload environment values.

Only nginx publishes a port, bound to loopback by default. Change `HTTP_PORT` if 8080 is occupied. Keep `HTTP_BIND_IP=127.0.0.1` unless deliberately allowing access from a trusted network.

## Environment variables

[.env.example](.env.example) is the Compose template. The Go process reads its environment once and does not load `.env` itself. Invalid configuration fails at startup without logging its values.

### Database and HTTP

| Variable | Required / default | Purpose |
|---|---|---|
| `POSTGRES_PASSWORD` | Required for Compose; replace template value | Initializes the database and constructs the app DSN |
| `DATABASE_URL` | Required for a direct Go process; supplied by Compose | PostgreSQL DSN; URL-encode credentials when constructing a direct URI |
| `HTTP_ADDR` | Go: `127.0.0.1:8080`; Compose: `:8080` | Backend listener; Compose fixes the internal port for nginx |
| `HTTP_BIND_IP` | `127.0.0.1` | Compose host interface |
| `HTTP_PORT` | `8080` | Compose host port |
| `TIME_ZONE` | `Europe/Moscow` | IANA zone for message-relative bot dates |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` (case-insensitive) |
| `HTTP_REQUEST_TIMEOUT` | `5s` | REST operation deadline; positive and below the fixed 15s write timeout |
| `STARTUP_TIMEOUT` | `30s` | Shared database readiness and migration deadline |

The standard Compose database is `finbot`, with user `finbot`; Go and PostgreSQL have no published host ports. Advanced pool/connection options belong in `DATABASE_URL` for direct execution or a Compose override.

### Telegram and LLM

| Variable | Required / default | Purpose |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | Optional, empty | Empty disables polling; required to use Telegram |
| `TELEGRAM_ALLOWED_USER_IDS` | Optional, empty | Comma-separated positive IDs; empty denies everyone; malformed entries prevent startup |
| `TELEGRAM_POLL_TIMEOUT` | `25s` | Whole seconds from 1s to 50s |
| `TELEGRAM_HTTP_TIMEOUT` | `40s` | Per-attempt HTTP timeout; must exceed the poll timeout |
| `TELEGRAM_RETRY_DELAY` | `1s` | Base delay for bounded API retries (at most three attempts) |
| `TELEGRAM_POLL_RETRY_DELAY` | `5s` | Delay before retrying a failed poll or unacknowledged update |
| `LLM_BASE_URL` | `https://openrouter.ai/api/v1` | Absolute HTTP(S) API prefix; adapter appends `/chat/completions` |
| `LLM_MODEL` | Go/Compose: `google/gemma-4-31b-it:free`; template: `openai/gpt-4.1-mini` | The explicit template value overrides the fallback when copied to `.env` |
| `LLM_API_KEY` | Optional, empty | Required by OpenRouter; may be omitted for a local endpoint |
| `LLM_TIMEOUT` | `30s` | Positive Go duration for one model request |

All duration settings must be positive. Allowlist IDs have no sign or leading zeros; surrounding whitespace and duplicate entries are accepted. The application uses one configured endpoint with no automatic cloud fallback or model retry. A local endpoint must be reachable **from the app container**; container `localhost` refers to that container.

### Optional proxy and frontend development

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_PROXY`, `HTTPS_PROXY` | Empty when unset | Standard Go outbound proxy settings, passed through Compose to `app` |
| `NO_PROXY` | Compose: `localhost,127.0.0.1,db` | Hosts that bypass the proxy |
| `FINBOT_DEV_API_URL` | `http://127.0.0.1:8080` | Vite development proxy target only |

Proxy settings mainly help in restricted networks. Leave them unset on networks with direct access; no VPN or Docker gateway address is required. Include any local model hostname in `NO_PROXY` when appropriate. No backend credentials belong in frontend `VITE_*` variables.

## Usage

Send private text messages to the bot from an allowed account:

| Action | Example |
|---|---|
| Create | `еда 500`, `кофе 350` |
| Relative date | `такси 1200 вчера` |
| List all dates | `мои расходы` |
| List today | `мои расходы за сегодня` |
| Last seven days | `покажи траты за неделю` |
| Monthly food total | `сколько я потратил на еду в этом месяце` |
| Correct a selected expense | `последнее было не 350, а 450` |
| Delete | `удали последнюю трату`, `удали такси` |

Before deletion, the bot shows the resolved expense and asks for `да` or `нет`. Selecting an ambiguous candidate still requires confirmation. Confirmation expires after 30 minutes; a panel change requires reselection. Standalone `стоп`, `отмена`, `отмени`, and `cancel` cancel pending work without changing expenses. Repeated cancellation and an affirmative without a pending confirmation are harmless. Cancellation words inside descriptions remain expense data.

Listing without a period, for example “мои расходы”, shows expenses across all dates. Creation and totals without a date default to today in `TIME_ZONE`, using the original Telegram message timestamp. “За неделю” includes today and the previous six days; “на этой неделе” starts on Monday. Relative dates are resolved by the server after model interpretation.

The current conversation focus takes precedence over the most recently created record. With no focus, “last” means `created_at DESC, id DESC`, regardless of purchase date. A mismatched previous amount or multiple matches prompts a text clarification; reply with the offered number or cancel in text. A stale focus after a web edit requires reselection. Focus expires after 30 minutes and is lost on restart.

In the panel, use **«Добавить расход»**, row edit/delete actions, and member/category/date filters. **«Участники семьи»** manages display names; clearing a name restores the numeric ID. Names never change ownership or bot permissions. Amounts have no currency label or conversion. The table scrolls horizontally on narrow screens.

## API overview

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/api/expenses` | Filtered, sorted, paginated list |
| `POST` | `/api/expenses` | Create an expense |
| `GET` | `/api/expenses/{id}` | Expense details |
| `PATCH` | `/api/expenses/{id}` | Update supplied fields |
| `DELETE` | `/api/expenses/{id}` | Delete; returns bodyless 204 |
| `GET` | `/api/members` | Discovered owners and saved member names |
| `PUT` | `/api/members/{user_id}` | Set or clear a display name |

IDs and amounts are JSON **strings**; `spent_at` is a calendar date (`YYYY-MM-DD`). Filters combine with AND; date boundaries are inclusive. See the [REST contract](docs/api.md) for envelopes, categories, validation, limits, and errors. `/readyz` is an internal app endpoint and is not exposed through the REST proxy.

## Development and testing

Use Go 1.27.1 and Node.js 24/npm. From the repository root:

```sh
cd backend
test -z "$(gofmt -l cmd internal migrations)"
go vet ./...
go test ./... -count=1 -timeout=180s
cd ../frontend
npm ci
npm run typecheck
npm test
npm run build
npm run dev
```

Vite serves `http://127.0.0.1:5173` and proxies `/api` to port 8080; the standard Compose installation can supply that backend. To use a different port, set `FINBOT_DEV_API_URL` before `npm run dev` or in `frontend/.env.local`.

### PostgreSQL integration tests

The race detector requires CGO and a C compiler. The database test role must be able to create schemas.

Without `TEST_DATABASE_URL`, database-dependent tests explicitly **skip**. Use a disposable database, never the development database. From the repository root, this example publishes only a loopback test port and uses no named volume (`--rm` removes the disposable container and its anonymous volume):

```sh
docker run -d --rm --name finbot-test-db \
  -e POSTGRES_USER=finbot_test \
  -e POSTGRES_PASSWORD=disposable-test-password \
  -e POSTGRES_DB=finbot_test \
  -p 127.0.0.1:15432:5432 postgres:17-alpine
until docker exec finbot-test-db pg_isready -U finbot_test -d finbot_test; do sleep 1; done
cd backend
TEST_DATABASE_URL='postgres://finbot_test:disposable-test-password@127.0.0.1:15432/finbot_test?sslmode=disable' \
  go test -race ./... -count=1 -timeout=180s
cd ..
docker stop finbot-test-db
```

Integration tests create and drop random schemas inside the selected database. They cover migrations, CRUD, exact totals, rollback, concurrency, ownership, duplicate updates, and the Telegram/LLM pipeline with local mock APIs. Automated tests make no live Telegram or paid model requests. Vitest covers forms, mutation safety, API errors, pagination, member names, and calendar/string precision.

Additional checks from the repository root:

```sh
docker compose config --quiet
cd frontend
TZ=America/Los_Angeles npm test -- src/validation.test.ts
TZ=Pacific/Kiritimati npm test -- src/validation.test.ts
```

The [CI workflow](.github/workflows/ci.yml) checks formatting/vet, the complete race-enabled Go suite with PostgreSQL 17, frontend tests/typecheck/build, timezone tests, both Docker images, Compose startup, and REST persistence across restart and recreation. A successful GitHub run has **not been verified** for this submission; the badge indicates configuration only. See the [QA report](docs/submission-readiness.md) for actual local results and remaining live acceptance.

## Security notes

- Bot authorization precedes model calls, stored-response reads, and operations. Ownership comes exclusively from Telegram `message.from.id`, never text, forwarded identity, or model arguments.
- Owner predicates protect bot reads, totals, updates, and deletes in SQL. SQL data values are parameterized; sort expressions use fixed allowlists.
- The panel and REST API intentionally have **no authentication**, as permitted by the assignment. Anyone with network access can administer all expenses, regardless of the bot allowlist. Keep the default loopback binding or use a trusted local network.
- Mutating browser requests validate Origin when present; this is not authentication. Reverse proxies must preserve Host. TLS termination and forwarded-header trust require deployment-specific configuration.
- Credentials are supplied through the environment. `.env` is ignored by Git. Application logs exclude credentials, message/prompt bodies, tool arguments, expense contents, and member names. The configured model provider receives message text and minimal context for the current user.

## Project structure

```text
backend/
  cmd/finbot/             Startup, readiness, graceful shutdown
  internal/
    expense/             Domain rules and application services
    postgres/            SQL repositories and Telegram transactions
    httpapi/             Administrative REST adapters
    telegram/            Bot API client and polling
    conversation/        Tool validation, focus, clarifications
    llm/                 OpenAI-compatible client
    config/              Environment validation
    observability/       Safe structured logging
  migrations/            Embedded Goose migrations
frontend/                React/TypeScript panel and component tests
scripts/smoke.py          Disposable-installation REST/persistence smoke
.github/workflows/       CI checks
docs/                    Assignment, contracts, architecture, QA
compose.yaml             Three-service local deployment
```

## Design decisions

- **Exact money:** integer hundredths in Go, decimal strings over JSON, and `NUMERIC(14,2)` in PostgreSQL. No floating-point money calculations; excess precision is rejected without rounding. SQL totals return decimal text, including totals beyond int64 hundredths.
- **Shared business rules:** transport parsing stays in HTTP/Telegram adapters; both workflows use the expense services and PostgreSQL repositories.
- **Atomic Telegram processing:** a unique `update_id`, expense mutation, and reply are committed together. Delivery failure replays the saved result without repeating the mutation; external API calls occur outside transactions.
- **Safe selections:** owner and `updated_at` guards are checked atomically when writing a deferred change. The model proposes actions while server code authorizes them.
- **Small deployment:** one Go process, one PostgreSQL database, and static React behind nginx. No users table, message broker, ORM, or separate agent service.

## Known limitations

- Live Telegram acceptance with allowed users A/B and denied C remains pending. Mock API tests and a real-browser panel run passed; they do not establish current provider interpretation or real-account delivery. See the [submission verdict](docs/submission-readiness.md#g-final-verdict).
- One sequential polling process; a permanently failed response delivery can block the queue. An uncertain send can repeat a reply, while the expense mutation remains idempotent.
- Conversation focus is in memory. Bot lists return the first 20 records and a total; more than 10 modification candidates require a narrower query. Description search is a literal comment substring.
- Saved Telegram replies have no retention cleanup or durable delivery outbox. Expense deletion does not erase historical saved replies.
- One family per installation, private text messages, one expense mutation per request, no currency conversion. Offset pages can shift during concurrent changes; a single response uses a consistent snapshot.
- Live local LLM verification is optional and unperformed. There is no automatic fallback when a configured provider fails.
