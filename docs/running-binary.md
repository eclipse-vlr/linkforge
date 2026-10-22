# Запуск LinkForge v0.1.0

Распакуй архив и перейди в его каталог. Папка `migrations` должна находиться рядом с исполняемым файлом: приложение читает её из текущего каталога при запуске.

Нужен PostgreSQL 17 с созданной базой. Redis 7 необязателен. При первом запуске приложение применяет миграции; пользователю базы нужны права на изменение схемы.

## Linux и macOS

```bash
export DATABASE_URL='postgres://user:password@127.0.0.1:5432/linkforge?sslmode=disable'
export BASE_URL='http://localhost:8080'
./linkforge
```

## Windows PowerShell

```powershell
$env:DATABASE_URL = 'postgres://user:password@127.0.0.1:5432/linkforge?sslmode=disable'
$env:BASE_URL = 'http://localhost:8080'
.\linkforge.exe
```

Замени пользователя, пароль и имя базы в `DATABASE_URL` своими значениями. Для удалённой базы настрой TLS по требованиям хостинга.

API доступен по адресу `http://localhost:8080`; проверка процесса — `GET /healthz`. Для другого порта задай `HTTP_ADDR`, например `:9090`, и соответствующий `BASE_URL`.

Для кеша задай `REDIS_URL`, например `redis://127.0.0.1:6379/0`. Для защиты создания ссылок задай `CREATE_LINK_API_KEY`; POST-запросы потребуют заголовок `X-API-Key`. Переменные задаются в окружении, `.env` автоматически не читается.

Ctrl+C корректно завершает сервер. Описание API находится в `openapi.yaml`, версия исходников — в `RELEASE_COMMIT`.
