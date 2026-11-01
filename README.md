# &

Одно приложение на Go: DPI-обход, Telegram MTProto-over-WebSocket и исправление web.telegram.org.

## Запуск

Linux/Windows: запустить `and` от root/администратора. Без повышенных прав работает только Telegram-прокси.

```bash
sudo ./and
```

Данные и движки разворачиваются в `data/` рядом с бинарником. Профили, списки и native-движки вшиты в бинарник.

## Сборка

```bash
go build -trimpath -o and ./cmd/and
```

Windows-сборка требует CGO и mingw. Всё остальное — CI.

:)
