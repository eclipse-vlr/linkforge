# LinkForge

[![Go CI](https://github.com/eclipse-vlr/linkforge/actions/workflows/go.yml/badge.svg)](https://github.com/eclipse-vlr/linkforge/actions/workflows/go.yml)

Сервис коротких ссылок на Go. Принимает длинный URL, выдаёт короткую ссылку и считает переходы.

На этом проекте разбираюсь с backend-разработкой на практике: HTTP, базой данных, кешем и тестированием. Стараюсь проверять и обычные сценарии, и то, что происходит при сбоях.

**Стек:** Go, `net/http`, PostgreSQL, Redis, Docker, GitHub Actions.

## Что внутри

- Ссылки и статистика хранятся в PostgreSQL, миграции применяются при запуске.
- Redis кеширует URL. Без него или при сбое кеша сервис обращается к базе.
- Создание ссылок ограничено по IP; при необходимости можно включить API-ключ.
- Есть проверка входных данных, логи, таймауты и корректная остановка сервера.

## Как запустить

Нужны Git, Go версии из [`go.mod`](go.mod) и Docker с Compose. Команды подходят для Linux, macOS и Windows PowerShell:

```bash
git clone https://github.com/eclipse-vlr/linkforge.git
cd linkforge
docker compose up -d --wait db
go run ./cmd/api
```

API будет доступен на `http://localhost:8080`. Запускай его из корня проекта — там лежат миграции. Для этого примера используются локальные настройки PostgreSQL по умолчанию, без Redis.

Redis подключается через `REDIS_URL`, а защита создания ссылок — через `CREATE_LINK_API_KEY`. Переменные задаются в окружении; `.env` автоматически не читается.

## Пример запроса

В другом окне терминала:

```bash
curl -i http://localhost:8080/api/v1/links \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/articles/go"}'
```

<details>
<summary>Тот же запрос в PowerShell</summary>

```powershell
Invoke-RestMethod -Uri http://localhost:8080/api/v1/links `
    -Method Post -ContentType 'application/json' `
    -Body '{"url":"https://example.com/articles/go"}'
```

</details>

В ответе придут `code`, `short_url` и `original_url`. Открой `short_url`, чтобы перейти на исходный адрес. Счётчик переходов доступен через `GET /api/v1/links/{code}/stats`.

Если задана `CREATE_LINK_API_KEY`, добавь к POST-запросу заголовок `X-API-Key` с её значением.

## Тесты

```bash
go test ./...
go vet ./...
```

Без `TEST_DATABASE_URL` и `TEST_REDIS_URL` соответствующие интеграционные тесты пропускаются. В [CI](.github/workflows/go.yml) они запускаются с настоящими PostgreSQL и Redis; там же проверяются гонки, уязвимости зависимостей и Docker-образ.

## Подробнее

- [Описание API](docs/api.md)
- [Запуск готовой сборки](docs/running-binary.md)
- [Docker и развёртывание](docs/deployment.md)
- [Нагрузочное тестирование](docs/benchmark.md)
- [Подготовка релиза v0.1.0](docs/release-v0.1.0.md)

Лимитер пока работает в памяти одного экземпляра, статистика публичная. Compose настроен для локальной разработки; для хостинга потребуются отдельные секреты и настройки доступа.

Лицензия пока не выбрана.
