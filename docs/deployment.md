# Docker и развёртывание

Для запуска нужны само приложение и PostgreSQL. Redis необязателен. Ниже — пример для компьютера и порядок настройки хостинга.

## Проверить Docker-образ локально

Выполни команды из корня репозитория:

```bash
docker build -t linkforge:local .
docker compose -p linkforge up -d --wait db
```

Теперь запусти API в той же Docker-сети, чтобы он мог найти базу по имени `db`:

```bash
docker run --rm --network linkforge_default -p 127.0.0.1:8080:8080 \
  -e DATABASE_URL='postgres://linkforge:devpassword@db:5432/linkforge?sslmode=disable' \
  -e BASE_URL='http://localhost:8080' linkforge:local
```

<details>
<summary>Команда запуска в PowerShell</summary>

```powershell
docker run --rm --network linkforge_default -p 127.0.0.1:8080:8080 `
  -e DATABASE_URL='postgres://linkforge:devpassword@db:5432/linkforge?sslmode=disable' `
  -e BASE_URL='http://localhost:8080' linkforge:local
```

</details>

API будет доступен на `http://localhost:8080`. Примеры запросов есть в [описании API](api.md).

## Разместить на Railway

1. Подключи репозиторий `eclipse-vlr/linkforge` к новому сервису и выбери сборку из корневого `Dockerfile`.
2. Добавь PostgreSQL с постоянным хранилищем. Передай его строку подключения в `DATABASE_URL` сервиса API.
3. Создай публичный домен и укажи его в `BASE_URL`, например `https://my-linkforge.up.railway.app`. Используй адрес без пути и параметров запроса.
4. Задай случайный `CREATE_LINK_API_KEY` в защищённых переменных. Он понадобится в заголовке `X-API-Key` при создании ссылок.
5. Для первого запуска оставь один экземпляр приложения и задай `/healthz` как путь проверки здоровья. После изменения переменных дождись перезапуска.

Railway передаёт порт через `PORT`; Docker-образ сам подставит его в `HTTP_ADDR`. Если задаёшь `HTTP_ADDR` вручную, используй `0.0.0.0:<порт>`, чтобы сервер принимал подключения извне контейнера.

| Переменная | Что указать |
|---|---|
| `DATABASE_URL` | Подключение созданного PostgreSQL, например `${{Postgres.DATABASE_URL}}` |
| `BASE_URL` | HTTPS-адрес приложения |
| `CREATE_LINK_API_KEY` | Случайный секрет для создания ссылок |
| `REDIS_URL` | Можно оставить пустым; для кеша нужен `redis://` или `rediss://` URL |

В ссылке `${{Postgres.DATABASE_URL}}` замени `Postgres` на имя своего сервиса базы. Пароли и ключ храни в переменных хостинга. Тестовый пароль из Compose оставь для локального запуска; для хостинга настрой отдельного пользователя базы и резервные копии.

## Проверить после запуска

Открой `/healthz`, создай ссылку с API-ключом, перейди по ней и посмотри статистику. Один `/healthz` показывает только работу HTTP-сервера — он не проверяет базу и кеш. Логи помогут увидеть ошибки подключения и миграций.

Лимитер хранится в памяти одного процесса и берёт IP из соединения. За одним прокси пользователи могут делить квоту. Статистика и переходы остаются публичными.
