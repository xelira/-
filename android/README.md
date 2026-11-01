# Android

Android-версия собирается локально из этого каталога. Она не использует root:
DPI работает через `VpnService`, ByeDPI и hev-socks5-tunnel.

## Что внутри

- `AndActivity` — точка входа Fyne поверх `org.golang.app.GoNativeActivity`.
- `DpiVpnService` — foreground `VpnService` в отдельном процессе `:dpi`.
- `NativeProxy` — JNI-обёртка над ByeDPI (`ciadpi`).
- `libhev-socks5-tunnel.aar` — tun2socks (MIT), поставляется как локальный AAR.
- `src/main/jni/byedpi` — исходники ByeDPI (MIT), собираются NDK.
- `ProxyKeepAliveService` — foreground-сервис, удерживающий Telegram-прокси.

Схема:

```text
Android VpnService / TUN
        ↓
hev-socks5-tunnel (C, TUN fd)
        ↓
ByeDPI / ciadpi 127.0.0.1:1080
        ↓
интернет
```

DNS не заворачивается в VPN: используются DNS-серверы нижележащей сети,
маршрут к ним исключён через `excludeRoute` (Android 13+). Это нужно, чтобы
ByeDPI не ломал DNS-запросы.

## Сборка

Только локально, с инструментами из `/mnt/storage/mobile-dev`:

```bash
source /mnt/storage/mobile-dev/env.sh
./scripts/build-android.sh
```

Скрипт:

1. копирует репозиторий в путь без `&` (`/mnt/storage/mobile-dev/and-build`);
2. собирает `liband.so` для `arm64-v8a`, `armeabi-v7a`, `x86_64`;
3. собирает `libbyedpi.so` через NDK;
4. собирает подписанный APK;
5. кладёт его в `dist/and-2.2.2 OBT.apk` и `/mnt/storage/mobile-dev/artifacts/`.

Нужны JDK 21, Android SDK 36, NDK 27.2.12479018, Gradle 8.14.3.
Пути задаются в `/mnt/storage/mobile-dev/env.sh`.

Для release-подписи создайте `android/keystore.properties`:

```properties
storeFile=/absolute/path/to/release.jks
storePassword=...
keyAlias=...
keyPassword=...
```

Файл добавлен в `.gitignore`. Без него Gradle соберёт APK debug-ключом.

## Ограничения Android

- Автозапуска нет: система требует явного согласия VPN.
- Системный `/etc/hosts` недоступен без root. Вкладка Telegram Web на Android
  показывает пояснение и кнопку открытия сайта.
- Одновременно может быть активен только один VPN. Если уже включён другой
  VPN, Android не даст включить DPI.
- По умолчанию UDP в ByeDPI отключён (`-U`) — браузеры переходят с QUIC на TCP.
  Telegram и веб работают через TCP; UDP-игры/QUIC могут не работать.
- `POST_NOTIFICATIONS` запрашивается при запуске, VPN — при первом включении DPI.

## Лицензии

- ByeDPI — MIT, см. `src/main/jni/byedpi/LICENSE`.
- hev-socks5-tunnel — MIT, см. `libs/hev-socks5-tunnel.LICENSE`.
- Fyne Java-activity — BSD-3-Clause, исходники Fyne.
