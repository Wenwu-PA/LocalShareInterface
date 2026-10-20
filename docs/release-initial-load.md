# Первая загрузка v0.1.0 RC

Команда: `.venv\Scripts\python.exe scripts/verify_go_initial_load.py`.

Холодный Chromium, `/`, первый запуск, ожидание шрифтов. Gzip level 9 над телами ответов; это оценка сжатого размера, не трафик HTTP (сервер не включает gzip).

| Ресурс | До, байт gzip | После, байт gzip |
|---|---:|---:|
| document | 322 | 350 |
| script | 58161 | 44984 |
| stylesheet | 6338 | 6338 |
| font | 96088 | 96088 |
| **Всего** | **160909** | **147760** |

Лимит: 150000 байт, включая шрифты. JS/CSS/HTML после изменения: 51672 байт.

| Чанк/ресурс после изменения | Байт gzip | При открытии `/` |
|---|---:|---|
| `/` | 350 | да |
| `/assets/index-DelC0LhE.css` | 6338 | да |
| `/assets/htm.module-BwEnn_k3.js` | 6452 | да |
| `/assets/index-CqlzBfMu.js` | 38532 | да |
| `/assets/inter-cyrillic-700-normal-CjBOestx.woff2` | 7927 | да |
| `/assets/inter-cyrillic-400-normal-obahsSVq.woff2` | 7735 | да |
| `/assets/inter-latin-700-normal-Yt3aPRUw.woff2` | 24317 | да |
| `/assets/inter-cyrillic-600-normal-CWCymEST.woff2` | 7995 | да |
| `/assets/inter-latin-400-normal-C38fXH4l.woff2` | 23669 | да |
| `/assets/inter-latin-600-normal-LgqL8muc.woff2` | 24445 | да |
| `devices-B137unNC.js` | 4089 | нет |
| `files-BRF6EYQ_.js` | 14034 | нет |
| `hash-worker-CqOUGBao.js` | 2799 | нет |
| `pdf-preview-BhSmu5Gq.js` | 129013 | нет |
| `pdf.worker.min-CjEcRF4W.mjs` | 375511 | нет |

Экраны файлов и устройств используют динамический импорт; PDF был ленивым ранее.
Функции и оформление не изменены. Первая загрузка здесь означает пустую сессию;
при сохранённой стартовой странице «Файлы» её чанк потребуется при входе.
Бинарник после сборки: 17056256 байт. RAM берётся как резидентная память процесса
(Windows Working Set); Private Bytes отдельно показана в предыдущем отчёте.

Финальная повторная оценка после маркировки RC: 147760 байт gzip.
