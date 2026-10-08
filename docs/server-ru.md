[English](server-en.md) | [Русский](server-ru.md) | [К README](../README-ru.md)

# SD Studio Server

Автономный Go-сервис для оркестрации AI-компонентов — Stable Diffusion WebUI, Ollama — с автоматической установкой, управлением жизненным циклом, мониторингом здоровья, GPU-оптимизацией и терминальным дашбордом.

## Обзор

SD Studio Server избавляет от ручной настройки AI-сервисов. Он устанавливает, запускает, мониторит и управляет всеми компонентами через единый конфигурационный файл, доступный через HTTP API или интерактивный TUI.

```
┌─────────────────────────────────────────────────┐
│              SD Studio Server                     │
├──────────┬───────────┬───────────┬──────────────┤
│ Process  │    GPU    │  Health   │     TUI      │
│ Manager  │ Monitor   │ Monitor   │  Dashboard   │
├──────────┴───────────┴───────────┴──────────────┤
│         GPU Proxy (очередь с приоритетами,       │
│         контроль VRAM)                           │
├─────────────────────────────────────────────────┤
│    HTTP API + mDNS discovery + Reverse Proxy     │
└─────────────────────────────────────────────────┘
```

## Установка

### Сборка из исходников

```bash
go build -o sd-studio-server .
```

### Docker

```bash
docker compose up --build
```

## Быстрый старт

```bash
# Первый запуск — интерактивный мастер настройки
./sd-studio-server --data ~/sd-studio-server

# Без TUI (headless-режим)
./sd-studio-server --headless

# Кастомный порт
./sd-studio-server --port 9090

# Кастомный конфиг
./sd-studio-server --config /path/to/server-config.yaml
```

При первом запуске мастер настройки проведёт через:
- Выбор компонентов для установки (SD WebUI, Ollama)
- Выбор директории данных
- Настройку GPU-бэкенда (Forge / A1111)

## Режимы запуска

| Режим | Команда | Описание |
|-------|---------|----------|
| TUI (интерактивный) | `./sd-studio-server` | Терминальный дашборд со статусом сервисов и управлением |
| Attach (удалённый) | `./sd-studio-server attach [host[:port]]` | Подключить TUI-дашборд к работающему демону (по умолчанию `127.0.0.1:8080`) |
| Headless | `./sd-studio-server --headless` | Только лог-вывод, без TUI — для серверов и Docker |
| Кастомный порт | `./sd-studio-server --port 9090` | Переопределить порт HTTP API |
| Кастомный конфиг | `./sd-studio-server --config path.yaml` | Использовать конкретный файл конфигурации |

### Режим attach

`attach` подключает интерактивный TUI-дашборд к **уже работающему** демону
(обычно `--headless` под systemd) через его REST API — например, с десктопа:

```bash
./sd-studio-server attach 192.168.1.184        # порт 8080 по умолчанию
./sd-studio-server attach 192.168.1.184:8080
```

- Тот же дашборд, что и локальный TUI: сервисы, health, GPU/VRAM, строка
  GPU-очереди и экран управления (`g`: приоритеты, cancel/release), логи
  процессов (`l`), старт/стоп/рестарт (`s`/`r`)
- Бары CPU/RAM показывают **хост демона** (блок `sys` в `/api/server/status`)
- Поллинг раз в 3с; при обрыве связи снапшот замораживается и появляется
  жёлтый баннер `reconnecting` (backoff до 30с) — клиент сам не выходит
- `q` / Ctrl+C завершает **только клиент**; демон продолжает работать
- Нужен TTY на stdin; недоступный демон → сразу ошибка, exit 1

## Конфигурация

Конфигурация хранится в `{data-dir}/server-config.yaml`:

```yaml
port: 8080
data_dir: ~/sd-studio-server
active_sd: forge
mdns: true
detected_vram_mb: 8192

gpu:
  total_budget_mb: 0        # 0 = авто: detected_vram_mb - reserve_mb; итог <= 0 отключает очередь
  reserve_mb: 500           # резерв под ОС/драйвер (только авто-режим)
  max_wait_seconds: 120     # лимит ожидания бюджета -> 503 + Retry-After
  lease_ttl_seconds: 90     # TTL аренды без heartbeat
  sd_default_weight_mb: 11000   # вес SD-джобы при неизвестном чекпоинте
  sd_overhead_mb: 4500      # вес = размер файла чекпоинта + этот overhead
  llm_weight_mb: 11000      # вес LLM-генерации (qwen-14b)

processes:
  python:
    name: "Python 3.10"
    autostart: false
    category: utility

  sd:
    name: "Stable Diffusion"
    health_url: "http://localhost:7860/sdapi/v1/options"
    target_url: "http://localhost:7860"
    proxy_path: "/api/sd/"
    autostart: true
    restart: true
    max_restart: 5
    env:
      PYTORCH_CUDA_ALLOC_CONF: "expandable_segments:True"

  ollama:
    name: "Ollama"
    binary: "ollama"
    args: ["serve"]
    health_url: "http://localhost:11434/api/tags"
    target_url: "http://localhost:11434"
    proxy_path: "/api/llm/"
    autostart: true
    restart: true
    max_restart: 5

backends:
  forge:
    name: "Stable Diffusion Forge"
    process_key: "sd"
    binary: "python/bin/python3"
    args: ["launch.py", "--listen", "--api", "--xformers", "--medvram-sdxl"]
    auto_optimize: true
    workdir: "stable-diffusion-webui-forge"
    models_dir: "stable-diffusion-webui-forge/models/Stable-diffusion"
    lora_dir: "stable-diffusion-webui-forge/models/Lora"
    vae_dir: "stable-diffusion-webui-forge/models/VAE"
    embedding_dir: "stable-diffusion-webui-forge/embeddings"
    install:
      method: zip
      url: "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip"
      target: "stable-diffusion-webui-forge"
      version: main

proxy:
  enabled: true
  gpu_slots: 1
  studio_header: "X-SD-Studio"
  endpoints:
    sd:
      listen_addr: ":7860"
      target_url: "http://localhost:7860"
    ollama:
      listen_addr: ":11434"
      target_url: "http://localhost:11434"
```

### Конфигурация процессов

| Поле | Описание |
|------|----------|
| `name` | Отображаемое имя |
| `binary` | Путь к исполняемому файлу (относительно data dir или абсолютный) |
| `args` | Аргументы командной строки |
| `env` | Переменные окружения |
| `workdir` | Рабочая директория |
| `health_url` | HTTP-эндпоинт для проверки здоровья |
| `target_url` | Целевой URL для обратного прокси |
| `proxy_path` | API-префикс для маршрутизации прокси |
| `autostart` | Автоматический старт при запуске сервера |
| `restart` | Автоматический перезапуск при сбое |
| `max_restart` | Максимальное количество перезапусков |
| `category` | Категория (пусто = основной, `utility` = вспомогательный) |

### Конфигурация установки

| Поле | Описание |
|------|----------|
| `method` | Метод установки: `zip`, `binary`, `pip`, `archive`, `tgz` |
| `url` | URL для скачивания |
| `target` | Целевая директория или имя пакета |
| `version` | Тег версии |

### Конфигурация бэкендов

| Поле | Описание |
|------|----------|
| `name` | Отображаемое имя |
| `process_key` | Ссылка на процесс в карте `processes` |
| `binary` | Переопределение бинарника для данного бэкенда |
| `args` | Аргументы запуска |
| `workdir` | Рабочая директория |
| `models_dir` | Директория SD-чекпоинтов |
| `lora_dir` | Директория LoRA-моделей |
| `vae_dir` | Директория VAE-моделей |
| `embedding_dir` | Директория текстуальных инверсий |
| `auto_optimize` | Автоматическая подстройка флагов на основе обнаруженного VRAM |

## Основные компоненты

### Менеджер процессов (`process/`)

Управляет жизненным циклом AI-сервисов как дочерних процессов.

- Запуск, остановка, перезапуск через API или TUI
- Автоматический перезапуск при сбое (настраиваемое число попыток)
- Захват и получение логов
- Отслеживание статуса (starting, running, stopped, failed)
- Корректное завершение с пересылкой сигналов

### GPU-монитор (`gpu/`)

Мониторинг GPU в реальном времени через nvidia-smi.

- Опрос каждые 5 секунд
- Отслеживает: имя GPU, память всего/использовано/свободно, утилизация %
- Предоставляет `OptimizerAdapter` для автоматической оптимизации флагов запуска SD на основе VRAM:
  - `--medvram-sdxl` для GPU с 8 ГБ
  - `--medvram` для GPU с 6 ГБ
  - `--lowvram` для GPU с 4 ГБ
- Корректно работает при отсутствии nvidia-smi

### GPU-прокси (`gpuproxy/`)

Обратный прокси с приоритетной очередью и управлением GPU-слотами.

- **Приоритетная очередь** — запросы с заголовком `X-SD-Studio` получают высокий приоритет
- **Ограничение GPU-слотов** — настраиваемое число параллельных GPU-задач
- **VRAM cooldown** — после завершения каждой задачи ожидает >= 50% свободной VRAM перед выдачей следующего слота (предотвращает OOM на GPU с малым VRAM)
- **Таймаут** — 30 секунд fallback для избежания вечной блокировки
- **Несколько эндпоинтов** — отдельные порты прокси для SD и Ollama

```
Клиент → Прокси (:7860) → Очередь → Слот → SD WebUI
Клиент → Прокси (:11434) → Очередь → Слот → Ollama
                    ↑
         Приоритет + контроль VRAM
```

### Монитор здоровья (`health/`)

Периодические HTTP-проверки всех настроенных сервисов.

- Настраиваемый интервал проверок
- Измерение задержки
- Отслеживание статуса (healthy/unhealthy)
- Результаты доступны через API и TUI

### Установщик (`installer/`)

Автоматическая установка всех компонентов.

- Скачивание и распаковка SD WebUI Forge
- Установка Python 3.10 standalone (платформо-зависимая)
- Проверка доступности бинарника Ollama
- Отчёт о прогрессе через коллбэки
- Отслеживание статуса установки

### mDNS-обнаружение (`mdns.go`)

Обнаружение сервисов в локальной сети.

- Рекламирует сервис `_sd-studio._tcp`
- Десктопные приложения могут автоматически находить сервер
- Настраивается через `mdns: true/false` в конфиге

### TUI-дашборд (`tui/`)

Интерактивный терминальный интерфейс на bubbletea.

- Обзор статуса сервисов с индикаторами здоровья
- Управление запуском/остановкой/перезапуском
- Панель GPU-информации (память, утилизация)
- Просмотр логов с прокруткой
- Отслеживание прогресса установки
- Мастер настройки первого запуска

## API-справочник

### Статус сервера

```
GET /api/server/status
```

Возвращает статус всех управляемых процессов:

```json
{
  "processes": {
    "sd": { "name": "Stable Diffusion", "status": "running", "pid": 12345, "uptime": "2h30m" },
    "ollama": { "name": "Ollama", "status": "running", "pid": 12340, "uptime": "2h30m" }
  }
}
```

### Управление процессами

```
POST /api/server/start/{name}
POST /api/server/stop/{name}
POST /api/server/restart/{name}
GET  /api/server/logs/{name}?lines=100
```

### Информация о GPU

Информация о GPU — часть ответа статуса сервера (поле `"gpu"`):

```
GET /api/server/status
```

```json
{
  "name": "NVIDIA GeForce RTX 3060",
  "memory_total_mb": 12288,
  "memory_used_mb": 4096,
  "memory_free_mb": 8192,
  "utilization_percent": 45,
  "available": true
}
```

### Модели

```
GET  /api/models                # Список доступных SD-моделей
POST /api/models/download       # Скачать модель по URL
DELETE /api/models/{name}       # Удалить модель
```

### Бэкенды

```
GET  /api/backends              # Список доступных бэкендов
POST /api/backends/switch       # Переключить активный бэкенд
```

### Здоровье

```
GET /api/health                 # Результаты проверки здоровья всех сервисов
```

### Маршруты прокси

```
/api/sd/*    → Stable Diffusion WebUI
/api/llm/*   → Ollama / LLM-сервис
```

## GPU-очередь

### Обзор

`gpuqueue` сериализует GPU-тяжёлую работу между службами (SD, LLM, внешними
воркерами) через взвешенный бюджет VRAM: джоба выполняется только когда
свободный бюджет ≥ её веса, иначе ждёт в FIFO-очереди (с ручным приоритетом).
Защищает от трэша/OOM VRAM при одновременной загрузке моделей Forge, Ollama
и yue-воркером.

- **Взвешенный admission**: тяжёлые пути прокси приобретают бюджет до проброса;
  лёгкие пути (options, progress, tags, ps) проходят свободно
- **Аренды с TTL + heartbeat**: истёкшая аренда освобождает бюджет автоматически
  (защита от мёртвого воркера); прокси-аренды продлеваются сами
- **fail-open**: любая внутренняя ошибка очереди = запрос проходит + warning в статусе
- **Ограниченное ожидание**: свыше `max_wait_seconds` → `503` + `Retry-After`
- Состояние in-memory: рестарт сервера = чистый лист
- Управление GPU-очередью: экран TUI по клавише `g` (локально и в attach)

### Веса

| Путь | Метод | Вес |
|------|-------|-----|
| `/api/sd/sdapi/v1/txt2img`, `img2img`, `interrogate` | POST | размер файла чекпоинта + `sd_overhead_mb` (чекпоинт трекается снифом `POST .../options`; неизвестен → `sd_default_weight_mb`); кламп до бюджета |
| `/api/llm/api/generate`, `/api/llm/api/chat` | POST | `llm_weight_mb` |
| всё прочее (options, progress, samplers, tags, ps, embeddings) | * | 0 (проходит свободно) |

### Lease-API (внешние GPU-потребители, например yue-воркер)

```
POST   /api/gpu/lease                 {kind: sd|llm|yue, client, weight_mb, priority?, wait_seconds?}
       -> 200 {id, acquired: true, ttl_seconds}
       -> 202 {id, acquired: false, position}
       -> 503 {"error": "gpu queue timeout"} + Retry-After (ожидание истекло)
       -> 503 {"error": "gpu queue full"}                     (очередь заполнена, 100)
GET    /api/gpu/lease/{id}            -> {status: active|queued|unknown, position, lease_deadline}
POST   /api/gpu/lease/{id}/heartbeat  -> 200 (только active) | 404
DELETE /api/gpu/lease/{id}            -> release/cancel -> 200 | 404
PATCH  /api/gpu/queue/{id}            {"move": "up"|"down"} -> 200 {position} | 400 | 404
GET    /api/gpu/status                -> {enabled, budget, running[], queue[], warnings[]}
```

Лимит тела 1 МБ; `client` обрезается до 32 символов; `wait_seconds` клампится
до `max_wait_seconds`.

### Граница доверия

Сервер только для LAN by design: без авторизации. ID аренд предсказуемы и без
проверки владельца — любой LAN-клиент может продлевать/отменять любую аренду.
`/api/gpu/status` раскрывает client-строки (из User-Agent). Не выставлять API
за пределы доверенной LAN.

## Деплой через Docker

```yaml
# docker-compose.yml
services:
  sd-studio-server:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/root/sd-studio-server
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
```

```bash
docker compose up --build -d
```

## Структура проекта

```
.
├── main.go              # Entrypoint (режимы TUI / headless / attach)
├── attach/              # Удалённый TUI-клиент (демон по REST)
├── handlers.go          # HTTP API-обработчики
├── proxy.go             # Обработчик обратного прокси
├── backends.go          # Логика переключения бэкендов
├── mdns.go              # mDNS-обнаружение сервисов
├── config/
│   ├── types.go         # Config, ProcessConfig, BackendConfig
│   ├── defaults.go      # Значения конфигурации по умолчанию
│   └── resolve.go       # Разрешение путей
├── gpu/
│   └── gpu.go           # Мониторинг GPU через nvidia-smi
├── gpuproxy/
│   ├── config.go        # Конфигурация прокси
│   ├── proxy.go         # Прокси с VRAM cooldown
│   ├── handler.go       # Обработчик reverse proxy на эндпоинт
│   └── queue.go         # Приоритетная очередь с управлением слотами
├── health/
│   └── monitor.go       # HTTP-проверки здоровья
├── installer/
│   └── installer.go     # Установка компонентов
├── models/
│   └── manager.go       # Управление SD/LLM моделями
├── process/
│   ├── manager.go       # Управление жизненным циклом процессов
│   └── types.go         # Типы статусов процессов
└── tui/
    ├── model.go         # Корневая модель bubbletea
    ├── dashboard.go     # Основной вид дашборда
    ├── wizard.go        # Мастер настройки первого запуска
    └── styles.go        # Стили TUI
```
