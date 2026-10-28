[English](README.md) · **فارسی**

<div dir="rtl">

# OpenVPN Client

یک برنامه‌ی دسکتاپ کوچک برای مدیریت و استفاده از اتصال‌های OpenVPN روی **Linux** و **macOS**. این برنامه رابط کاربری ساده‌ای برای همان OpenVPN نصب‌شده روی سیستم شماست: پروفایل‌های `.ovpn` را وارد کنید، با یک کلیک وصل و قطع کنید و ترافیک زنده را ببینید.

- ورود پروفایل با drag & drop یا انتخاب فایل؛ هر بار فقط یک اتصال فعال
- وضعیت زنده، IP روی VPN، اینترفیس، مدت اتصال، routeها، DNS و نمودار لحظه‌ای ترافیک
- اعلان‌های دسکتاپ، اتصال مجدد خودکار، لاگ‌ها با فیلتر و خروجی گرفتن
- تم روشن/تاریک، اندازه‌ی پیش‌فرض پنجره شبیه گوشی، منوی پنهان
- هیچ رمزی ذخیره نمی‌شود؛ برنامه هرگز با دسترسی root اجرا نمی‌شود (فقط برای راه‌اندازی OpenVPN مجوز می‌گیرد)

ساخته‌شده با Go، [Wails v3](https://wails.io)، Vue 3، TypeScript و Tailwind CSS.

## Screenshots

<details>
<summary>نمایش تصاویر</summary>

| بدون پروفایل | لیست پروفایل‌ها |
|---|---|
| <img src="art/main-page-no-profile.png" width="320" alt="Empty state with import area"> | <img src="art/main-page-profile-sample.png" width="320" alt="Profile list"> |

| متصل | لاگ‌ها |
|---|---|
| <img src="art/profile-connected-sample.png" width="320" alt="Connected profile with live traffic chart"> | <img src="art/logs-page.png" width="320" alt="Logs page"> |

| تنظیمات (۱/۲) | تنظیمات (۲/۲) |
|---|---|
| <img src="art/settings-page-1.png" width="320" alt="Settings, first half"> | <img src="art/settings-page-2.png" width="320" alt="Settings, second half"> |

</details>

## دانلود

نصب یا به‌روزرسانی با یک دستور (Linux و macOS؛ بسته‌ی `.deb`، `.rpm` یا برنامه‌ی macOS مناسب سیستم را انتخاب می‌کند):

</div>

```sh
curl -fsSL https://raw.githubusercontent.com/mrmmg/openvpn-desktop/main/install.sh | sh
```

<div dir="rtl">

بسته‌های آماده در صفحه‌ی [Releases](https://github.com/mrmmg/openvpn-desktop/releases) هستند: `.deb`، `.rpm` و `.tar.gz` برای Linux (amd64) و `.dmg`/`.zip` یونیورسال برای macOS.

برنامه می‌تواند نسخه‌ی جدید را به شما اطلاع دهد (About ← Check for updates یا Settings ← Updates) و همین دستور را نشان می‌دهد.

برنامه‌ی macOS هنوز با Apple Developer ID امضا نشده است؛ برای اولین اجرا روی آن راست‌کلیک و **Open** را بزنید (یا `xattr -cr /Applications/openvpn-desktop.app` را اجرا کنید).

## پیش‌نیازها

- OpenVPN (دستور `openvpn` در `PATH` باشد، یا مسیرش را در Settings بدهید)
- **Linux:** polkit همراه با یک authentication agent (KDE و GNOME دارند)
- **macOS:** چیز اضافه‌ای لازم نیست؛ هنگام اتصال پنجره‌ی رمز سیستم نمایش داده می‌شود

## ساخت از روی سورس

به Go نسخه‌ی 1.27 به بالا، Node نسخه‌ی 20 به بالا و [Wails v3 CLI](https://v3.wails.io/getting-started/installation/) نیاز دارید (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0`). روی Linux همچنین `gcc`، `pkg-config` و بسته‌های توسعه‌ی GTK 4 و WebKitGTK 6، و روی macOS ابزارهای خط فرمان Xcode. با `wails3 doctor` وضعیت را بررسی کنید.

</div>

```sh
wails3 dev        # development with hot reload
wails3 build      # production binary in bin/
wails3 package    # AppImage/deb/rpm (Linux) or .app (macOS)
go test ./...     # tests (add -short to skip the OpenVPN integration tests)
```

<div dir="rtl">

## ساختار پروژه

| مسیر | محتوا |
|---|---|
| `main.go` | برنامه‌ی Wails، پنجره و اعلان‌ها |
| `internal/backend` | کتابخانه‌ی پروفایل‌ها، کنترل اتصال، لاگ‌ها و تنظیمات (متصل به UI) |
| `internal/vpn`, `mgmt`, `process`, `privilege` | چرخه‌ی عمر OpenVPN از طریق management interface، اجراکننده‌ی pkexec/osascript |
| `internal/appconfig` | فایل کوچک `config.yaml` برای تنظیمات برنامه |
| `frontend/` | رابط Vue (`src/`)؛ پوشه‌ی `bindings/` را Wails تولید می‌کند |

تنظیمات در `~/.config/openvpn-desktop/` ذخیره می‌شود (در macOS: `~/Library/Application Support/openvpn-desktop/`). پروفایل‌های واردشده در پوشه‌ی `profiles/` کپی می‌شوند که مسیرش را می‌توانید در Settings عوض کنید. فایل‌های اصلی شما هرگز تغییر نمی‌کنند.

## انتشار و نسخه‌بندی

نسخه‌ها از [Semantic Versioning](https://semver.org/) پیروی می‌کنند و از git tag گرفته می‌شوند؛ تغییرات در [CHANGELOG.md](CHANGELOG.md) هست. برای انتشار، ابتدا یک مورد به changelog اضافه کنید، سپس:

</div>

```sh
git tag v0.2.0 && git push origin v0.2.0
```

<div dir="rtl">

GitHub Actions بسته‌های Linux و macOS را می‌سازد و release را ایجاد می‌کند (تگی با پسوند مثل `v0.2.0-rc.1` به‌صورت pre-release منتشر می‌شود). در build محلی، نسخه از `git describe` گرفته می‌شود.

## وضعیت

روی Linux توسعه و تست شده است. مسیرهای مخصوص macOS فقط build می‌شوند و هنوز روی Mac اجرا نشده‌اند.

</div>
