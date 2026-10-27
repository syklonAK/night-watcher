# Night Watcher — وضعیت پروژه و راهنمای ادامهٔ توسعه

آخرین به‌روزرسانی: **2026-10-28**. این فایل نقطهٔ شروع توسعه‌های بعدی است؛ ابتدا آن را بخوانید و سپس فقط فایل‌های مرتبط با تغییر موردنظر را بررسی کنید. اطلاعات این فایل جای بررسی کدِ تغییریافته را نمی‌گیرد.

## تصمیم‌های قطعی کاربر

- **سیستم‌عامل هدف فقط Linux است**؛ معماری‌های amd64 و arm64. رفع ایرادهای مخصوص Windows جزو هدف پروژه نیست.
- زبان اجرای محصول Go است؛ Python daemon یا Python runtime نباید وابستگی محصول باشد.
- باینری پیش‌فرض با `CGO_ENABLED=0` ساخته شود. ابزار سیستمی `nft` برای اعمال فایروال لازم است؛ فرمان‌های مجاز ترمینال نیز باید روی میزبان موجود باشند.
- نام محصول Night Watcher و مقصد updater برابر `syklonAK/night-watcher` است.
- هفت ماژول Edge باید به هسته و پنل موجود متصل باشند، نه صرفاً یک سرویس نمایشی جداگانه.
- این فایل پس از تغییر معماری، رفتار، آزمون‌ها یا محدودیت‌ها به‌روز شود؛ کار تکمیل‌شده و کار تأییدنشده جدا ثبت شوند.

## محل سورس و منشأ

| مورد | مسیر / مقدار |
|---|---|
| ریشهٔ سورس فعلی | `E:/nw/Arange-tun-main`؛ نام پوشه هنوز تاریخی است |
| آرشیو پایه | `E:/001-vibe-codeing/0001-nw/Arange-tun-main.zip` |
| آرشیو donor | `E:/001-vibe-codeing/watrouter-edge-suite.zip` |
| donor استخراج‌شده | `E:/nw/donor/watrouter-edge-suite` |
| Go module | `github.com/syklonAK/night-watcher` |
| نسخهٔ Go در go.mod | `1.24.4`؛ به‌دلیل وابستگی‌های فعلی به 1.22 کاهش ندهید |
| نسخهٔ محصول | `VERSION` هنوز نسخهٔ پایهٔ `v1.31.1` است؛ نسخهٔ انتشار جدید تعیین نشده |
| تنظیمات Linux | `/etc/night-watcher`؛ فایل Edge: `edge.json` |
| مسیر نصب باینری | `/usr/local/bin/night-watcher` |

در بررسی قبلی این پوشه مخزن Git نبود؛ commit، push یا انتشار انجام نشده است. LICENSE و NOTICE برای حفظ انتساب پروژهٔ پایه نگه داشته شده‌اند. اسکریپت‌های Python در ریشهٔ `E:/nw` ابزارهای یک‌بارمصرف ویرایش بوده‌اند و بخشی از runtime محصول نیستند؛ بازاجرای آن‌ها ایمن یا idempotent فرض نشود.

راهنمای عملیاتی و قرارداد دقیق تنظیمات در [docs/native-edge.md](docs/native-edge.md) و نمونهٔ تنظیمات در [examples/edge.json](examples/edge.json) است.

## معماری و مالکیت منابع

`internal/edge.Manager` تنظیمات معتبر، router، balancer، health workers، snapshot و lifecycle را هماهنگ می‌کند. یک Manager در هر process وجود دارد؛ state حافظه بین processهای مستقل مشترک نیست.

- process پنل `--webui`: مالک listenerهای ورودی Edge، جدول فایروال و Web Shell است.
- process موتور `-c` و process `--proxy`: تنظیمات مشترک Edge را می‌خوانند و policy خروجی SOCKS5/HTTP proxy را اعمال می‌کنند.
- uplinkهای transport و port mappingهای ثابت هسته، تنظیمات transport خودشان را دارند. policy Edge به‌طور بازگشتی روی تمام ارتباطات داخلی هسته اعمال نشده است.
- برای عبور از tunnel، یک upstream از نوع `forward` می‌تواند به پورت محلیِ exposed شدهٔ آن tunnel اشاره کند.
- reload فایل هر دو ثانیه انجام می‌شود؛ `SIGHUP` reload فوری درخواست می‌کند. reload نامعتبر تنظیمات فعال را حفظ کرده و `last_error` ثبت می‌کند.
- connectionهای موجود با route قبلی ادامه می‌یابند؛ اتصال‌های جدید policy جدید را می‌گیرند. shutdown اتصال‌های تحت مدیریت و sessionهای ترمینال را لغو می‌کند.
- توقف پنل listenerها را می‌بندد، اما isolation فایروال را حذف نمی‌کند. حذف آگاهانه با غیرفعال‌کردن firewall و apply تنظیمات انجام می‌شود.

## نقشهٔ ماژول‌های Edge

| ماژول | فایل‌های اصلی | رفتار پیاده‌سازی‌شده |
|---|---|---|
| Bootstrap / detection | `internal/edge/netdetect.go`, `netdetect_linux.go` | WAN انتخاب‌شده توسط kernel، آدرس و MTU اینترفیس، public IPv4/IPv6 با HTTPS، raw ICMP RTT و PMTU محدود به 1500؛ refresh پنج‌دقیقه‌ای |
| Metrics | `internal/edge/metrics.go` | atomic counters، نرخ اینترفیس‌ها، وضعیت socketهای میزبان، snapshot تونل‌ها، JSON و Prometheus |
| Routing | `internal/edge/routing.go`, `listener.go` | ruleهای domain/IP/CIDR/network/port، bypass، direct/block/group؛ TCP، TLS SNI passthrough و HTTP/HTTPS ingress |
| HA / failover | `internal/edge/balancer.go` | TCP/HTTP HEAD/ICMP probes، آستانه‌های موفقیت/شکست، smooth weighted round robin با RTT، تلاش مجدد روی node سالم |
| Firewall | `internal/edge/firewall.go` | wrapper فرمان `nft` بدون shell، اعتبارسنجی قبل از batch، جدول اختصاصی dual-stack، kill switch، DNS allowlist و جلوگیری از loop |
| Web Shell | `internal/edge/shell.go`, `shell_linux.go` | PTY، profile محلی فرمان، UID/GID غیرroot، ticket یک‌بارمصرف، timeout، لغو process group |
| Lifecycle | `internal/edge/manager.go`, `config.go`, `signals_unix.go` | strict config، reload، snapshot، save/backup/restore و cancellation |

هر ماژول فایل‌های `*_test.go` دارد؛ تست PTY لینوکس در `shell_linux_test.go` است و در شرایط فاقد دسترسی لازم skip می‌شود.

## نقاط اتصال به کد پایه

| مسیر | مسئولیت |
|---|---|
| `internal/webui/handlers_edge.go` | APIهای Edge، کنترل auth/origin، backup و shell WebSocket |
| `internal/webui/server.go` | ایجاد Manager مالک، ثبت routeها، graceful shutdown |
| `internal/webui/assets/edge.html` | صفحهٔ فارسی Edge، KPI، chart، جداول، JSON editor، preview فایروال، backup/restore و console |
| `internal/webui/assets/dashboard.html` | لینک ورود به `/edge` |
| `internal/webui/prometheus.go` | افزودن metrics Edge به `/metrics` |
| `internal/webui/compress.go` | عبور WebSocket Upgrade بدون gzip برای حفظ Hijacker |
| `internal/socks/socks5.go`, `udpassociate.go` | اعمال dialer Edge در خروجی SOCKS5 |
| `internal/localproxy/` | اعمال policy در HTTP CONNECT و HTTP forward |
| `cmd/` | راه‌اندازی Edge در موتور/proxy، SIGHUP و نمونه‌برداری metrics هر دو ثانیه |
| `internal/metrics/` | نوشتن snapshot با atomic write |
| `internal/app/` | نام محصول/مخزن/مسیرها و atomic write |
| `internal/packet/engine/socket/` | abstraction capture و backend بومی Linux AF_PACKET |
| `install.sh`, `Makefile`, updater | نام‌ها و build پیش‌فرض CGO=0 |
| `.github/workflows/ci.yml` | تست Ubuntu، race و build لینوکس amd64/arm64؛ نتیجهٔ اجرای CI هنوز موجود نیست |
| `.github/workflows/release.yml` | نام‌گذاری assetهای انتشار Night Watcher |

backend پیش‌فرض packet از AF_PACKET استفاده می‌کند و به libpcap/C compiler نیاز ندارد. اجرای آن `CAP_NET_RAW` یا root لازم دارد. backend قدیمی فقط با tag `pcap` و CGO فعال قابل ساخت است. رفتار واقعی AF_PACKET روی Linux هنوز باید آزموده شود.

## قراردادهایی که در تغییرات بعدی باید حفظ شوند

- precedence routing: اولین rule صریح، سپس bypass CIDR، سپس default.
- `*.example.com` فقط subdomain را می‌گیرد؛ apex باید جدا تعریف شود. CIDR برای مقصد IP literal است؛ hostname برای تطبیق CIDR به‌طور محلی resolve نمی‌شود.
- SOCKS5 upstream فعلاً TCP است. UDP فقط direct یا fixed forward دارد؛ UDP پشتیبانی‌نشده نباید بی‌صدا به direct تبدیل شود.
- وقتی همهٔ nodeها unavailable باشند، route گروه fail می‌شود؛ fallback مستقیم ممنوع است. node جدید تا رسیدن به success threshold آماده نیست.
- upstreamهای ingress از نوع `forward` هستند. HTTP/HTTPS ingress به backend HTTP وصل می‌شود؛ upstream TLS خودکار، gRPC/h2 proxy و تبدیل آزاد nginx config پیاده‌سازی نشده‌اند.
- تغییر mode روی همان پورت listener اشغال‌شده نیازمند توقف listener قبلی است.
- JSON تنظیمات strict و محدود به 1 MiB است؛ فایل با atomic write و mode 0600 نوشته می‌شود.
- shell به‌صورت پیش‌فرض غیرفعال است. profile فرمان، UID/GID و directory از تنظیمات محلی تعیین می‌شوند؛ Edge config/restore API نباید scope آن را ارتقا دهد.
- ticket ترمینال 32 بایت تصادفی، یک‌بارمصرف، متصل به session و profile و با اعتبار 30 ثانیه است. انتقال در WebSocket subprotocol است، نه URL.
- shell علاوه بر session و Origin یکسان، HTTPS یا اتصال loopback از طریق SSH forwarding لازم دارد. PTY به‌تنهایی sandbox نیست.
- تنها جدول `inet night_watcher_edge` تحت مالکیت این کد است؛ global flush انجام نشود. DNS filtering پورت 53/853 معادل تشخیص DoH نیست.
- metrics scopeهای process، interface، host و tunnel را تفکیک می‌کند. counter اتصال ردشده با packet loss یکی نیست؛ دادهٔ ناموجود نباید موفقیت یا loss صفر تفسیر شود.

## APIها

| مسیر | متد و دسترسی |
|---|---|
| `/edge` | صفحهٔ پنل با session |
| `/api/edge/metrics`, `/api/edge/state` | GET؛ session یا bearer فقط‌خواندنی پنل |
| `/api/edge/metrics?format=prometheus`, `/metrics` | متن Prometheus؛ JSON endpoint مستقیماً فرمت Prometheus نیست |
| `/api/edge/config` | GET/POST؛ admin session |
| `/api/edge/reload` | POST؛ admin session |
| `/api/edge/backup` | GET؛ فقط تنظیمات Edge |
| `/api/edge/restore` | POST؛ strict JSON و حفظ scope shell |
| `/api/edge/firewall/preview` | POST؛ نمایش plan |
| `/api/shell/token` | POST؛ دریافت ticket برای profile مجاز |
| `/api/shell/ws` | WebSocket؛ session + Origin + ticket |

mutationهای Edge نیازمند same-origin و JSON هستند. backup کامل برنامه با backup اختصاصی Edge متفاوت است؛ نکتهٔ بازبینی امنیتی آن در بخش کارهای باقی‌مانده ثبت شده است.

## وضعیت واقعی اعتبارسنجی

این بخش گزارش اجرای موجود است، نه ادعای آمادگی production. میزبان فعلی Windows است و محیط اجرای Linux/WSL یا container در دسترس تأییدشده وجود ندارد. **هدف محصول همچنان فقط Linux است.**

| بررسی | آخرین نتیجهٔ ثبت‌شده |
|---|---|
| Linux amd64، `CGO_ENABLED=0 go build -o night-watcher main.go` | موفق؛ بازسازی 2026-10-28 شامل تمام تغییرات امروز |
| Linux arm64، `CGO_ENABLED=0 go build -o night-watcher-arm64 main.go` | موفق 2026-10-28 |
| تست‌های edge/webui/metrics/socks/localproxy روی میزبان فعلی | موفق 2026-10-28 پس از آخرین تغییر metrics |
| تست‌های manage (backup shell scope, path traversal) و telegram | موفق 2026-10-28 |
| go vet روی بسته‌های تغییرکرده | بدون خطا 2026-10-28 |
| syntax JavaScript صفحات Edge و dashboard | `node --check` موفق 2026-10-28 |
| تست کامل Linux، race | هنوز نتیجهٔ اجرای تأییدشده ثبت نشده |
| تست واقعی nft، AF_PACKET، PTY و PMTU روی Linux | انجام نشده؛ compile یا mock جایگزین این تست‌ها نیست |
| بررسی بصری صفحه در مرورگر | انجام نشده؛ اتصال Chrome فراهم نشد |

اجرای قبلی `go test ./...` روی Windows کامل موفق نبود: تفاوت errno، permission، نام interface و timeout دخیل بودند. این خطاها اثبات خرابی Linux نیستند و به‌درخواست کاربر نباید مسیر توسعه را به رفع Windows منحرف کنند.

**آخرین patch اعتبارسنجی شد (2026-10-28):** تفکیک `RejectedFlows` از packet drop و `LossPercent` تونل در `internal/edge/metrics.go` — gofmt اجرا شد، تست/vet/build موفق. UI و `docs/native-edge.md` هماهنگ شدند: کارت flows شمارش جریان‌های ردشده، جدول تونل‌ها ستون Packet Loss (درصد KCP، متمایز از drop اینترفیس) و توضیح scope در مستند. باینری `night-watcher` بازسازی‌شده از سورس تأییدشدهٔ فعلی است؛ اعتبارسنجی runtime روی Linux همچنان انجام نشده.

**دورزدن scope شل از مسیر restore کامل، شناسایی و اصلاح شد (2026-10-28):** `manage.Restore` پیش‌تر `edge.json` را از archive بی‌قید و شرط می‌نوشت و چون Edge Manager فایل را هر دو ثانیه بازمی‌خواند، import کامل backup از `/api/backup/import` می‌توانست گارد `saveEdge` روی ShellConfig را دور بزند. اکنون `restoreEdgeConfig` در `internal/manage/backup.go` بخش `shell` فایل محلی را هنگام بازنویسی حفظ می‌کند (هم‌سیاست با Edge API؛ میزبان جدید بدون فایل محلی archive را کامل می‌گیرد). تست‌ها در `internal/manage/backup_shell_test.go`: حفظ shell محلی در برابر archive مخرب، نوشتن verbatim وقتی فایل محلی نیست و رد path traversal. تست/vet روی manage/webui/edge و build لینوکس موفق.

**خطای rollback فایروال اصلاح شد (2026-10-28):** در `internal/edge/manager.go` مسیر `apply`، خطای rollback پس از شکست `SaveConfig` با `_ =` نادیده گرفته می‌شد؛ یعنی اگر قوانین جدید اعمال شده و ذخیره فایل شکست خورده و rollback هم شکست بخورد، اختلاف قوانین زنده و فایل config بی‌صدا می‌ماند. اکنون خطای rollback در پیام خطا ترکیب و با راهنمای بازبینی دستی nft گزارش می‌شود. تست `TestManagerSaveReportsFailedRollback` در `internal/edge/manager_rollback_test.go` با `failRunner` (شمارش race-safe) سناریوی fail در call چهارم (rollback) را می‌سازد. تست edge موفق.

**rebrand متن‌های انسانی بازبینی شد (2026-10-28):** نام قدیمی فارسی «آرنج‌تان» از `README_FA.md` (عنوان و ۴ نکته)، `internal/webui/assets/dashboard.html` (۹ مورد در رشته‌های ترجمه و راهنماها) و `internal/telegram/lang.go` (پیام انتشار نسخه) با «Night Watcher» جایگزین شد؛ کامنت «the way BackPack asks» در dashboard حذف و ارجاع اشتباه «Existing Arange installations» در `docs/native-edge.md` به «donor project» اصلاح شد. ذکر تاریخی ARANGE در CHANGELOG (سابقهٔ fork) و attribution fork در README/LICSense/NOTICE عمداً حفظ شدند. gofmt، تست telegram/edge/manage/webui، `node --check` هر دو صفحه و build لینوکس amd64/arm64 موفق.

## کارهای باقی‌مانده، به ترتیب اولویت

1. روی Linux تمام تست‌ها و race را اجرا کنید؛ سپس nft/kill switch/DNS IPv4+IPv6، PTY revocation و AF_PACKET را در میزبان یا namespace ایزوله آزمایش کنید. روی میزبان فعالِ راه‌دور بدون راه بازیابی فایروال آزمایش نکنید. این کار به محیط Linux (میزبان، WSL یا container) نیاز دارد که روی Windows فعلی در دسترس نیست.
2. رفتار reload/SIGHUP/SIGTERM در چند process روی Linux و بررسی بصری صفحه‌های پنل در مرورگر تأیید شود.
3. پس از تأیید موارد بالا، نسخهٔ release و بستهٔ تحویل تعیین شود. هیچ release یا deploy واقعی انجام نشده است.

## دستورهای مرجع برای Linux

از ریشهٔ سورس اجرا شوند:

```sh
gofmt -w internal/edge/metrics.go internal/edge/routing.go internal/edge/listener.go internal/edge/routing_test.go
CGO_ENABLED=0 go test ./internal/edge ./internal/webui ./internal/metrics ./internal/socks ./internal/localproxy
CGO_ENABLED=0 go vet ./internal/edge ./internal/webui ./internal/metrics ./internal/socks ./internal/localproxy
CGO_ENABLED=0 go test ./... -timeout 10m
CGO_ENABLED=1 go test -race ./internal/edge ./internal/webui ./internal/socks -timeout 5m
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -o night-watcher main.go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o night-watcher-arm64 main.go
```

race detector ابزار توسعه است و C toolchain می‌خواهد؛ CGO فعال در تست race، شرط باینری production با CGO غیرفعال را تغییر نمی‌دهد. cross-compile موفق به معنی اجرای موفق تست‌ها روی Linux نیست.

برای ادامه در همین workspace، Go قبلاً در `C:/Users/Daniyal/go-toolchain/go/bin/go.exe` موجود بود؛ cacheها `E:/nw/.cache/go-build` و `E:/nw/.cache/go-mod` و `GOTOOLCHAIN=local` استفاده شده‌اند. این مسیرها فقط جزئیات محیط توسعه‌اند و نباید وارد config محصول Linux شوند.

## حدود تطبیق با donor

`watrouterd.py` عمدتاً Nginx/HAProxy/UFW و WatTunnel خارجی را مدیریت می‌کرد. قابلیت‌های Edge در Go بازپیاده‌سازی شده‌اند؛ این به معنی سازگاری کامل پروتکل و فایل‌های donor نیست. آرشیو donor پیاده‌سازی transport وات‌تانل را ندارد. `wat1:`، YAML/keyهای WatTunnel، carrier auto-selection و Cloudflare ECH tuning انتقال کامل نشده‌اند. تبدیل configهای آزاد Nginx/HAProxy یا `state.json` donor نیازمند migration صریح است.

## روش نگهداری این فایل

پس از هر مرحلهٔ توسعه، تاریخ، فایل‌های اصلی تغییرکرده، رفتار جدید، فرمان و نتیجهٔ واقعی validation و موارد باز را به‌روز کنید. موارد حل‌شده را از صف باز حذف و نتیجهٔ تست آن‌ها را ثبت کنید. برای شروع بعدی، ابتدا «کارهای باقی‌مانده» و «وضعیت واقعی اعتبارسنجی» را بخوانید؛ نیازی به تحلیل مجدد کل سورس نیست.
