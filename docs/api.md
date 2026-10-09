# API LANBridge

[README](../README.md) · [Безопасность](security.md)

Полная схема работающего сервера: [Swagger UI](https://localhost:8765/docs), [OpenAPI JSON](https://localhost:8765/openapi.json). Адрес изменяется вместе с портом/hostname. Код запросов находится в [api_example.py](../scripts/api_example.py); он проверяется на одноразовой базе, не содержит готового пароля и не меняет файлы пользователя.

## Аутентификация

1. `GET /` устанавливает bootstrap-cookie `lanbridge_csrf`.
2. `POST /api/setup` создаёт первого администратора, если база пуста; иначе `POST /api/login` выполняет вход. JSON-поля: `username`, `password`. В обоих случаях отправьте значение CSRF-cookie в `X-CSRF-Token`.
3. Ответ устанавливает `lanbridge_session` и новую `lanbridge_csrf`. Для последующих изменяющих запросов используйте новое значение.
4. `POST /api/logout` завершает текущую сессию. Пароль передавайте только через HTTPS, не в query string.

Проверка без аутентификации на Windows:

```powershell
curl.exe --insecure https://localhost:8765/api/status
```

Клиент с вводом пароля без отображения:

```powershell
python scripts/api_example.py --self-signed
```

`--self-signed` отключает проверку сертификата в этом клиенте; используйте его только для собственного локального сервера. С доверенным сертификатом запускайте без флага. Пользователь должен уже существовать.

## Основные маршруты

| Метод и путь | Доступ | Назначение |
|---|---|---|
| `GET /api/status` | Любой локальный клиент | Нужен ли setup; состояние текущей сессии |
| `GET /api/me` | admin/user | Текущий пользователь |
| `GET, POST /api/users` | admin | Список/создание пользователей |
| `DELETE /api/users/{username}` | admin | Удаление обычного пользователя |
| `POST /api/account/password` | admin/user | Смена своего пароля |
| `GET /api/files` | admin/user | `path`, `q`, `sort=name/size/modified/type`, `order=asc/desc` |
| `POST /api/files/folders` | admin/user | Создание каталога: `path` |
| `POST /api/files/rename` | admin/user | `path`, `new_name` |
| `DELETE /api/files` | admin/user | Удаление: JSON `path` |
| `GET /api/files/download` | admin/user | `path`; файл либо ZIP каталога |
| `GET /api/files/preview` | admin/user | `path`; поддерживаемый безопасный предпросмотр |
| `GET /api/transfers` | admin/user | `limit` 1–500, `offset`, `q`, `status`, `direction`; user видит свои записи |
| `GET /api/audit` | admin | `limit` 1–500, `offset`, `q` |
| `GET /api/devices`, `POST /api/devices/scan` | admin/user | Список и обновление ARP-наблюдений |
| `GET /api/metrics`, `GET /api/alerts` | admin/user | Текущие показатели и предупреждения |
| `GET /api/metrics/history` | admin/user | `window=hour/day/week` |
| `GET /api/diagnostics` | admin/user | Ping, DNS, маршрут; может выполняться несколько секунд |
| `POST /api/diagnostics/speed` | admin/user | Двоичное тело до 64 МиБ; UI использует 8 МиБ |
| `GET /api/qr` | admin/user | PNG data URL и текущий origin |
| `GET /api/external` | admin | Провайдер, готовность, URL, статус и счётчики |
| `POST /api/external/toggle` | admin | JSON `enabled: true/false` |
| `GET /api/external/check` | admin | Внешний IP и подсказки NAT; делает внешний HTTP-запрос |

`status` истории: `active`, `paused`, `completed`, `interrupted`, `cancelled`; `direction`: `upload`, `download`, `guest`. CSV формируется браузером из результатов API, отдельного CSV-endpoint нет.

## Передача частями

Запрос начала:

```http
POST /api/uploads/start
Content-Type: application/json
X-CSRF-Token: значение-cookie
Cookie: lanbridge_session=токен; lanbridge_csrf=значение-cookie

{"path":"documents/report.txt","size":15,"modified":1791496800000}
```

Поля ответа: `upload_id`, `chunk_size`, `received_chunks`, `resumed`. `modified` — `File.lastModified` браузера в миллисекундах. Размер части выбирает сервер.

| Шаг | Запрос |
|---|---|
| Узнать полученные части | `GET /api/uploads/{upload_id}` |
| Отправить часть | `PUT /api/uploads/{upload_id}/chunks/{index}` с `application/octet-stream` и CSRF |
| Пауза | `POST /api/uploads/{upload_id}/pause`, JSON `{"paused":true}` |
| Продолжить | Тот же endpoint с `false` либо повторный start с прежними метаданными |
| Завершить | `POST /api/uploads/{upload_id}/complete` |
| Отменить | `DELETE /api/uploads/{upload_id}` |

Индексы начинаются с нуля. Все части, кроме последней, имеют ровно `chunk_size` байт. Повторная отправка принятой части допустима с тем же содержимым; конфликтующий SHA-256 отклоняется. Загрузка принадлежит создавшему пользователю. Нулевой файл завершается без отправки чанков. Ответ завершения содержит итоговый `sha256`; для независимой проверки сравните его с хешем исходного файла.

Для Range-скачивания используйте авторизованный GET с `Range: bytes=0-99`; ответ — `206`, `Content-Range` и 100 байт. Обычный GET возвращает `200`; диапазон вне файла — `416`. Multi-range не поддерживается.

## Гостевые ссылки

`POST /api/shares` принимает `path`, `ttl_hours` (1–720), `max_downloads` (1–10000), необязательный `password` (8–256 символов при использовании). Ответ содержит готовый `url`, `token_hash` для отзыва и параметры срока. URL строится из адреса текущего запроса: для внешней ссылки создавайте её после входа через внешний origin.

`GET /api/shares` возвращает ссылки пользователя либо все ссылки для admin. `DELETE /api/shares/{token_hash}` доступен только admin. Исходный bearer-токен нельзя восстановить из БД.

Получатель открывает `/s/{token}`. Страница обращается к `GET /api/public/{token}?path=...`; если нужен пароль, получает `password_required`. `POST /api/public/{token}/unlock` с JSON `password` и bootstrap-CSRF выдаёт HttpOnly-cookie `lanbridge_guest`. Чтение: `GET /api/public/{token}/download?path=...`. Для каталога путь относится только к нему; `..` и выход за его пределы отклоняются. Каждый запрос скачивания расходует лимит, даже если это Range или клиент прервал чтение.

## WebSocket и ошибки

`wss://host:port/api/ws` использует ту же сессию и Origin. Сообщения имеют `type: metrics` с CPU, памятью, диском, интерфейсами и активными передачами либо `type: alert` с сообщением и временем. Истёкшую сессию при новом подключении сервер не принимает.

| HTTP-код | Причина |
|---|---|
| 400 | Недопустимый путь, имя или параметр операции |
| 401 | Нет действующей сессии/неверный пароль |
| 403 | Недостаточные права, CSRF, Origin или запрет сетевого источника |
| 404 | Объект недоступен; ссылка отозвана, истекла или исчерпала лимит |
| 409 | Имя занято, конфликт чанка или отсутствуют части |
| 413 | Слишком большой файл/тело запроса |
| 416 | Неподдерживаемый или недопустимый Range |
| 422 | Ошибка валидации Pydantic |
| 429 | Лимит попыток входа/пароля |

Endpoint переключения туннеля возвращает JSON состояния с `enabled:false` и `status: Ошибка` при неудачном запуске; HTTP 200 здесь не означает, что доступ включён. Всегда проверяйте тело ответа.
