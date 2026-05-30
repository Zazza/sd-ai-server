# SD Studio Server

[English](README.md)

Автономный Go-сервис для оркестрации AI-компонентов — Stable Diffusion WebUI, Ollama, Rembg — с автоматической установкой, управлением жизненным циклом, мониторингом здоровья, GPU-оптимизацией и терминальным дашбордом.

## Обзор

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

## Быстрый старт

```bash
# Сборка
go build -o sd-studio-server .

# Первый запуск — интерактивный мастер настройки
./sd-studio-server --data ~/sd-studio-server

# Без TUI (headless-режим)
./sd-studio-server --headless

# Кастомный порт
./sd-studio-server --port 9090
```

## Режимы запуска

| Режим | Команда | Описание |
|-------|---------|----------|
| TUI (интерактивный) | `./sd-studio-server` | Терминальный дашборд со статусом сервисов |
| Headless | `./sd-studio-server --headless` | Только лог-вывод — для серверов и Docker |
| Кастомный порт | `./sd-studio-server --port 9090` | Переопределить порт HTTP API |
| Кастомный конфиг | `./sd-studio-server --config path.yaml` | Использовать конкретный файл конфигурации |

## Конфигурация

Хранится в `{data-dir}/server-config.yaml`. Ключевые поля:

```yaml
port: 8080
data_dir: ~/sd-studio-server
active_sd: forge
mdns: true
```

Полный справочник конфигурации: [docs/server-ru.md](docs/server-ru.md).

## Основные компоненты

| Компонент | Пакет | Описание |
|-----------|-------|----------|
| Менеджер процессов | `process/` | Управление жизненным циклом AI-сервисов |
| GPU-монитор | `gpu/` | Мониторинг GPU в реальном времени через nvidia-smi |
| GPU-прокси | `gpuproxy/` | Обратный прокси с приоритетной очередью и контролем VRAM |
| Монитор здоровья | `health/` | Периодические HTTP-проверки сервисов |
| Установщик | `installer/` | Автоматическая установка компонентов |
| mDNS-обнаружение | `mdns.go` | Обнаружение сервисов в локальной сети |
| TUI-дашборд | `tui/` | Интерактивный терминальный интерфейс (bubbletea) |

## API-справочник

| Эндпоинт | Метод | Описание |
|----------|-------|----------|
| `/api/server/status` | GET | Статус всех процессов |
| `/api/server/start/{name}` | POST | Запустить процесс |
| `/api/server/stop/{name}` | POST | Остановить процесс |
| `/api/server/restart/{name}` | POST | Перезапустить процесс |
| `/api/server/logs/{name}` | GET | Логи процесса (`?lines=100`) |
| `/api/gpu` | GET | Информация о GPU (имя, память, утилизация) |
| `/api/models` | GET | Список доступных SD-моделей |
| `/api/models/download` | POST | Скачать модель по URL |
| `/api/models/{name}` | DELETE | Удалить модель |
| `/api/backends` | GET | Список доступных бэкендов |
| `/api/backends/switch` | POST | Переключить активный бэкенд |
| `/api/health` | GET | Результаты проверки здоровья |
| `/api/sd/*` | * | Прокси → Stable Diffusion WebUI |
| `/api/llm/*` | * | Прокси → Ollama |
| `/api/rembg/*` | * | Прокси → Rembg |

## Docker

```bash
docker compose up --build -d
```

## Структура проекта

```
.
├── main.go              # Entrypoint (режимы TUI / headless)
├── handlers.go          # HTTP API-обработчики
├── proxy.go             # Обработчик обратного прокси
├── backends.go          # Логика переключения бэкендов
├── mdns.go              # mDNS-обнаружение сервисов
├── config/              # Типы и значения конфигурации
├── gpu/                 # Мониторинг GPU через nvidia-smi
├── gpuproxy/            # Приоритетный прокси с контролем VRAM
├── health/              # HTTP-проверки здоровья
├── installer/           # Установка компонентов
├── models/              # Управление SD/LLM моделями
├── process/             # Управление жизненным циклом процессов
└── tui/                 # Терминальный дашборд (bubbletea)
```

## Документация

- [Full documentation (English)](docs/server-en.md)
- [Полная документация (Русский)](docs/server-ru.md)
