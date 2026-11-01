# &

Одно приложение для трёх платформ: DPI-обход, Telegram MTProto-over-WebSocket
и исправление web.telegram.org.

## Платформы

| Платформа | DPI | Telegram-прокси | hosts |
|---|---|---|---|
| Linux | nftables + nfqws, нужен root | да | да |
| Windows | WinDivert + winws, нужен администратор | да | да |
| Android | VpnService + ByeDPI, без root | да | нет (системный hosts недоступен) |

## Linux / Windows

```bash
sudo ./and
```

Данные и движки разворачиваются в `data/` рядом с бинарником. Профили, списки
и native-движки вшиты в бинарник.

## Android

Собирается локально с инструментами в `/mnt/storage/mobile-dev`:

```bash
source /mnt/storage/mobile-dev/env.sh
./scripts/build-android.sh
```

APK: `dist/and-2.2.2 OBT.apk`.

Android-версия не требует root:

- DPI — `VpnService` + `hev-socks5-tunnel` + ByeDPI;
- Telegram — встроенный MTProto-прокси на `127.0.0.1:1443`;
- автозапуска нет;
- системный hosts недоступен, вкладка Web показывает пояснение;
- при первом включении DPI нужно разрешить VPN, уведомления запрашиваются
  при запуске.

Подробности: [`android/README.md`](android/README.md).

## Сборка desktop

```bash
# Linux
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.Version=2.2.2 OBT" -o and ./cmd/and

# Windows (CGO + mingw)
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w -X main.Version=2.2.2 OBT" -o and.exe ./cmd/and
```

Windows и Linux также собираются в CI.

## Лицензии

- ByeDPI — MIT.
- hev-socks5-tunnel — MIT.
- Fyne — BSD-3-Clause (с локальным патчем для frameless-окна).
