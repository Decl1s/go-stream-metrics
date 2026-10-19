# Фактические технологии

| Компонент | Версия | Назначение и причина выбора | Официальный источник |
| --- | --- | --- | --- |
| Go | 1.27.2, минимум в go.mod; локально 1.27.2 linux/amd64 | Компилируемый CLI и потоковое чтение | https://go.dev/dl/?mode=json |
| bufio.Scanner | Стандартная библиотека Go 1.27.2 | Построчный вход с явным максимальным буфером | https://pkg.go.dev/bufio |
| io.Reader, LimitReader, TeeReader | Стандартная библиотека | Ограничение входа и хэширование прочитанных байтов без полной копии | https://pkg.go.dev/io |
| encoding/json | Стандартная библиотека | Разбор токенов и строгая проверка трёх полей, вывод сводки | https://pkg.go.dev/encoding/json |
| SHA-256 | crypto/sha256 стандартной библиотеки | Проверка входных байтов; hashlib Python — для исходного ZIP | https://pkg.go.dev/crypto/sha256 |
| flag, os и filepath | Стандартная библиотека | Параметры, обычные файлы, исключительное создание результата | https://pkg.go.dev/os |
| testing, go test и go vet | Инструменты Go 1.27.2 | Тесты, примеры сбоев и статическая проверка | https://pkg.go.dev/testing |
| JSONL и JSON | UTF-8, целые длительности | Один объект на строку; фиксированная схема результата | https://pkg.go.dev/encoding/json |
| Python | 3.11–3.12; локально 3.12.3 | Только проверка, упаковка и сценарий собранного демо | https://docs.python.org/3.12/ |
| ZIP, hashlib, unittest | Стандартная библиотека Python | Архив по явному списку, контрольная сумма, проверка публикации | https://docs.python.org/3.12/library/zipfile.html |
| GNU time | Локальный инструмент /usr/bin/time, Linux | Одноразовый сравнительный замер максимального RSS | https://www.gnu.org/software/time/ |
| Git | 2.x | Изолированная история проекта и проверка списка отслеживаемых файлов | https://git-scm.com/docs |
| GitHub Actions | Планируются Linux и Windows с Go 1.27.2, Python 3.12 | Тесты, собранное демо и исходный архив | https://docs.github.com/ru/actions |
| actions/checkout | v4, SHA в ci.yml | Получение исходников без сохранения учётных данных | https://github.com/actions/checkout |
| actions/setup-go | v6, SHA 924ae3a1cded613372ab5595356fb5720e22ba16 | Закреплённая версия Go, без кэша зависимостей | https://github.com/actions/setup-go |
| actions/setup-python | v5, SHA в ci.yml | Python для вспомогательных проверок | https://github.com/actions/setup-python |
| actions/upload-artifact | v4, SHA в ci.yml | Сохранение ZIP и SHA256SUMS | https://github.com/actions/upload-artifact |

Прямых и транзитивных сторонних библиотек Go и Python нет. В go.mod только модуль
и версия Go; go.sum не нужен. Rust, C-компилятор, Docker, база и сервер не используются.
CI задаёт CGO_ENABLED=0, GOPROXY=off, GOSUMDB=off и GOTOOLCHAIN=local.
Сборка и обработка не требуют скачивания модулей. Получение самого Go и публикация
используют сеть. Версия Go закреплена в go.mod и CI; более новый компилятор может
изменить характеристики производительности и памяти, что требует повторного замера.

Системный Go 1.22.2 для разработки этого проекта не использовался: Go 1.27.2
распакован только в .tools/go. SHA-256 официального linux/amd64 архива проверена:
`ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5`.
Эта папка и кэш сборки исключены из Git и исходного архива.

Пакеты Go: bufio, bytes, crypto/sha256, encoding/hex, encoding/json, errors, flag,
fmt, io, math, os, path/filepath, reflect, regexp, sort, strconv, strings, testing,
unicode/utf8. Модули Python: argparse, hashlib, importlib, json, os, pathlib,
re, subprocess, sys, tempfile, unittest и zipfile.
