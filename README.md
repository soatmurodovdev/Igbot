# igbot — Instagram kommentlariga ochiq javob yozuvchi bot

Go bilan yozilgan. Webhook orqali yangi kommentlarni oladi, PostgreSQL navbatiga yozadi,
tasniflaydi (qoidalar + ixtiyoriy AI) va Instagram API orqali **faqat ochiq javob** yozadi. DM yubormaydi.

## MUHIM (halol ogohlantirish)
- Bu kod yozilgan muhitda Go kompilyatori yo'q edi, shuning uchun kod **kompilyatsiya qilib tekshirilmagan**.
  Birinchi xatoni Cloud Run build loglarida ko'rasiz. Xato chiqsa, logni menga yuboring, tuzataman.
- Meta API: javob `POST /{comment-id}/replies`, webhook maydoni `comments`, ruxsat (Instagram Login uchun)
  `instagram_business_manage_comments`. Bular rasmiy hujjatdan tekshirilgan, lekin Meta ularni o'zgartirishi mumkin.
- Public Reply uchun aniq limitlarni Meta hujjatlarida o'zingiz ham tekshiring. Kodda `MIN_SEND_INTERVAL_MS`
  bilan tezlik cheklangan, lekin bu Meta limiti emas, shunchaki ehtiyotkor sozlama.
- "$0" kafolatlanmaydi: Cloud Run karta (billing) talab qiladi, Neon/Supabase va AI'ning o'z limitlari bor.
  Byudjet ogohlantirishini albatta yoqing.

## Fayllar (hammasi bitta papkada)
| Fayl | Vazifasi |
|---|---|
| main.go | Dasturni ishga tushiradi |
| config.go | Muhit o'zgaruvchilari (sozlamalar) |
| server.go | Webhook, /tasks/run, admin va test endpointlari |
| worker.go | Navbatni qayta ishlash, retry, tezlikni boshqarish |
| classify.go | Til va kategoriya aniqlash, spam filtri |
| replybank.go + replies.json | Javoblar banki (replies.json'ni tahrirlab yangilaysiz) |
| ai.go | Ixtiyoriy AI javob (AI_ENABLED=true bo'lsa) |
| instagram.go | Instagram API: javob yozish, token yangilash |
| db.go | PostgreSQL jadvallari va so'rovlar |
| classify_test.go | Oddiy testlar |
| Dockerfile | Cloud Run uchun |

## Muhit o'zgaruvchilari
Majburiy: `DATABASE_URL`, `VERIFY_TOKEN`, `APP_SECRET`, `CRON_SECRET`, `ADMIN_TOKEN`,
va `DRY_RUN=false` bo'lsa `IG_ACCESS_TOKEN`. Qolganlari `.env.example` faylida.

- `DRY_RUN=true` — haqiqiy javob YUBORILMAYDI (xavfsiz boshlash).
- `TEST_MODE=true` — /test/... endpointlarini yoqadi. Real ishga o'tganda `false` qiling.
- `APP_SECRET` — Instagram Login ishlatilsa, Meta panelidagi **Instagram app secret** (Meta app secret'dan farq qilishi mumkin).
- Tokenlarni GitHub'ga yozmang. Faqat Cloud Run'dagi Variables/Secrets'ga kiriting.

## Qanday ishlaydi
1. Instagram `/webhook` ga komment yuboradi. Server imzoni tekshiradi, kommentni bazaga yozadi (`comment_id` UNIQUE) va darhol "EVENT_RECEIVED" deydi.
2. Cloud Scheduler har daqiqada `POST /tasks/run` ni chaqiradi. Worker navbatdan kommentlarni oladi, tasniflaydi, javob tanlaydi va yuboradi.
3. Statuslar: QUEUED → PROCESSING → REPLIED / SKIPPED / FAILED / RETRY.
4. Vaqtinchalik xato bo'lsa, exponential backoff bilan qayta uriniladi (MAX_ATTEMPTS gacha), keyin FAILED.
5. Token xatosi yoki limit xatosida komment yo'qolmaydi, keyinroq qayta uriniladi.
6. Yuborish boshlanib, natija noma'lum qolsa, qayta yuborishdan oldin Instagram'da shu javob bormi tekshiriladi.

## Cloud Scheduler sozlamasi
- URL: `https://SERVIS-URL/tasks/run`, usul: POST, chastota: `* * * * *`
- Header: `X-Cron-Secret: <CRON_SECRET qiymati>`
- Cloud Run: min instances = 0, max instances = 2 yoki 3.

## Test (telefon brauzerida, TEST_MODE=true, DRY_RUN=true)
- Tasnif: `https://SERVIS-URL/test/classify?token=ADMIN_TOKEN&text=Assalomu alaykum`
- Sun'iy komment: `https://SERVIS-URL/test/comment?token=ADMIN_TOKEN&text=Juda chiroyli`
- Takroriy test: bir xil `&id=abc` bilan ikki marta chaqiring, ikkinchisida `inserted: false` chiqadi.
- 100 ta komment: `https://SERVIS-URL/test/flood?token=ADMIN_TOKEN&n=100`
- Navbatni ishga tushirish: Scheduler'da "Force run" tugmasi.
- Statistika: `https://SERVIS-URL/admin/stats?token=ADMIN_TOKEN`
- Tarix qidiruvi: `https://SERVIS-URL/admin/comments?token=ADMIN_TOKEN&status=REPLIED&q=rahmat`

Eslatma: `?token=` URL'da yuradi va Cloud Run so'rov loglarida ko'rinishi mumkin. Sinovdan keyin `ADMIN_TOKEN`ni almashtiring.
"test-" bilan boshlangan kommentlarga hech qachon haqiqiy javob yuborilmaydi.

## Haqiqiy ishga o'tish
1. Real kommentlar bilan `DRY_RUN=true` da sinang, `/admin/comments` da javoblarni ko'zdan kechiring.
2. `TEST_MODE=false`, `DRY_RUN=false` qiling.
3. Bitta postda sinab ko'ring, keyin hammasiga yoying.
