# SD Studio Server

[English](README.md)

Автономный Go-сервис для оркестрации AI-компонентов — Stable Diffusion WebUI, Ollama — с автоматической установкой, управлением жизненным циклом, мониторингом здоровья, GPU-оптимизацией и терминальным дашбордом.

> **Примечание:** поддержка Rembg удалена в v1.1.0 — в новых версиях SD Studio он не используется.
> Последний релиз с поддержкой Rembg — [v0.5.0](https://github.com/Zazza/sd-ai-server/releases/tag/v0.5.0).

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
| Attach (удалённый) | `./sd-studio-server attach 192.168.1.184` | Подключить TUI-дашборд к работающему headless-демону; `q` — отключиться, демон продолжает работу |
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
| GPU-очередь | `gpuqueue/` | Взвешенный бюджет VRAM с FIFO-очередью, арендами, Lease-API |
| GPU-прокси | `gpuproxy/` | Устаревший standalone-прокси с контролем VRAM (выключен по умолчанию) |
| Монитор здоровья | `health/` | Периодические HTTP-проверки сервисов |
| Установщик | `installer/` | Автоматическая установка компонентов |
| mDNS-обнаружение | `mdns.go` | Обнаружение сервисов в локальной сети |
| TUI-дашборд | `tui/` | Интерактивный терминальный интерфейс (bubbletea) |

## API-справочник

| Эндпоинт | Метод | Описание |
|----------|-------|----------|
| `/api/server/status` | GET | Статус всех процессов + информация о GPU |
| `/api/server/start/{name}` | POST | Запустить процесс |
| `/api/server/stop/{name}` | POST | Остановить процесс |
| `/api/server/restart/{name}` | POST | Перезапустить процесс |
| `/api/server/logs/{name}` | GET | Логи процесса (`?lines=100`) |
| `/api/gpu/status` | GET | Статус GPU-очереди (бюджет, running, очередь, warnings) |
| `/api/gpu/lease` | POST | Аренда GPU для внешних воркеров |
| `/api/models` | GET | Список доступных SD-моделей |
| `/api/models/download` | POST | Скачать модель по URL |
| `/api/models/{name}` | DELETE | Удалить модель |
| `/api/backends` | GET | Список доступных бэкендов |
| `/api/backends/switch` | POST | Переключить активный бэкенд |
| `/api/health` | GET | Результаты проверки здоровья |
| `/api/sd/*` | * | Прокси → Stable Diffusion WebUI |
| `/api/llm/*` | * | Прокси → Ollama |

## Docker

```bash
docker compose up --build -d
```

## Структура проекта

```
.
├── main.go              # Entrypoint (режимы TUI / headless / attach)
├── attach/              # Удалённый TUI-клиент (подключение к демону по REST)
├── handlers.go          # HTTP API-обработчики
├── proxy.go             # Обработчик обратного прокси
├── backends.go          # Логика переключения бэкендов
├── mdns.go              # mDNS-обнаружение сервисов
├── config/              # Типы и значения конфигурации
├── gpu/                 # Мониторинг GPU через nvidia-smi
├── gpuproxy/            # Устаревший standalone-прокси (выключен по умолчанию)
├── gpuqueue/            # Бюджет GPU-очереди (взвешенный семафор, аренды)
├── health/              # HTTP-проверки здоровья
├── installer/           # Установка компонентов
├── models/              # Управление SD/LLM моделями
├── process/             # Управление жизненным циклом процессов
└── tui/                 # Терминальный дашборд (bubbletea)
```

## Документация

- [Full documentation (English)](docs/server-en.md)
- [Полная документация (Русский)](docs/server-ru.md)
