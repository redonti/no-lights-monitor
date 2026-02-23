package bot

// All user-facing bot messages in one place.

// ── /start & /help ──────────────────────────────────────────────────

const msgStart = `<b>Вітаю в No-Lights Monitor!</b>

Я допоможу моніторити стан електроенергії у вашому домі та сповіщати Telegram-канал, коли світло зникає або повертається.

/create - Налаштувати новий монітор
/info - Детальна інформація та URL для пінгу
/edit - Змінити назву або адресу монітора
/test - Відправити тестове повідомлення
/stop - Призупинити моніторинг
/resume - Відновити моніторинг
/delete - Видалити монітор
/help - Детальніше

💬 Питання, ідеї? @lights_monitor_chat`

const msgHelp = `<b>Як це працює:</b>

1. Використайте /create для реєстрації нового монітора
2. Вкажіть адресу — я автоматично знайду координати
3. Створіть Telegram-канал і додайте мене як адміністратора
4. Я дам вам унікальне посилання для пінгу
5. Ваш пристрій пінгує це посилання кожні 5 хвилин
6. Якщо пінги зупиняються — я сповіщаю канал, що світла немає
7. Коли пінги відновлюються — сповіщаю, що світло є

<b>Команди:</b>
/info — детальна інформація та URL для пінгу
/edit — змінити назву або адресу монітора
/test — відправити тестове повідомлення в канал
/stop — призупинити моніторинг (не буде сповіщень)
/resume — відновити призупинений монітор
/delete — видалити монітор назавжди
/cancel — скасувати поточну операцію

💬 Питання, ідеї? @lights_monitor_chat`

// ── Generic / errors ────────────────────────────────────────────────

const (
	msgError           = "Щось пішло не так. Спробуйте пізніше."
	msgErrorRetry      = "Щось пішло не так. Спробуйте ще раз."
	msgCancelled       = "Операцію скасовано."
	msgInvalidFormat   = "Невірний формат"
	msgInvalidMonitor  = "Невірний ID монітора"
	msgMonitorNotFound = "Монітор не знайдено"
	msgFetchError      = "Помилка отримання даних"
	msgUnknownAction   = "Невідома дія"
)

// ── /status ─────────────────────────────────────────────────────────

const (
	msgNoMonitors        = "У вас ще немає моніторів.\n\nСтворіть перший через /create"
	msgStatusPaused      = "⏸ Призупинено"
	msgInfoStatusOnline  = "🟢 Онлайн"
	msgInfoStatusOffline = "🔴 Офлайн"
)

// ── /stop & /resume ─────────────────────────────────────────────────

const (
	msgStopHeader       = "<b>Призупинити моніторинг</b>\n\nОберіть монітор для зупинки:\n\n"
	msgNoActiveMonitors = "У вас немає активних моніторів для зупинки.\n\nВикористайте /resume, щоб відновити призупинені монітори."

	msgResumeHeader       = "<b>Відновити моніторинг</b>\n\nОберіть монітор для відновлення:\n\n"
	msgNoInactiveMonitors = "У вас немає призупинених моніторів.\n\nВикористайте /stop, щоб призупинити монітор."
)

// ── /delete ─────────────────────────────────────────────────────────

const (
	msgDeleteHeader     = "<b>⚠️ Видалення монітора</b>\n\nОберіть монітор для видалення:\n\n<i>Увага: ця дія незворотна! Всі дані про історію статусу будуть втрачені.</i>\n\n"
	msgNoMonitorsDelete = "У вас немає моніторів для видалення."
)

// ── /test ───────────────────────────────────────────────────────────

const (
	msgTestHeader     = "<b>Надіслати тестове повідомлення</b>\n\nОберіть монітор для відправки тесту:\n\n"
	msgNoTestChannels = "У вас немає моніторів з налаштованими каналами.\n\nСпочатку створіть монітор через /create та вкажіть канал."
)

// ── /info ───────────────────────────────────────────────────────────

const msgInfoHeader = "<b>Детальна інформація про монітори</b>\n\n"

// ── Callbacks ───────────────────────────────────────────────────────

const (
	msgStopOK    = "✅ Моніторинг призупинено"
	msgStopError = "Помилка зупинки моніторингу"

	msgResumeOK          = "✅ Моніторинг відновлено"
	msgResumeError       = "Помилка відновлення моніторингу"
	msgResumeNoAccess       = "❌ Бот не має доступу до каналу"
	msgResumeNoAccessDetail = "❌ <b>Не вдалося відновити моніторинг</b>\n\nБот не є адміністратором каналу <b>@%s</b> або не має права публікувати повідомлення.\n\nДодайте бота як адміністратора з правом \"Публікація повідомлень\" і спробуйте ще раз."

	msgDeleteOK    = "✅ Монітор видалено"
	msgDeleteError = "Помилка видалення монітора"

	msgTestOK            = "✅ Тест відправлено"
	msgTestNoChannel     = "У цього монітора немає каналу"
	msgTestSendError     = "Помилка відправки тестового повідомлення"
	msgStartOverRequired = "Почніть заново через /create"
)

// ── /create flow ────────────────────────────────────────────────────

const msgCreateStep1 = `Налаштуємо новий монітор!

<b>Крок 1/3:</b> Оберіть тип моніторингу:`

const msgCreateBtnHeartbeat = "📡 ESP або смартфон"
const msgCreateBtnPing = "🌐 Пінг айпі роутера"

const msgPingTargetStep = `<b>Крок 2/4:</b> Введіть IP-адресу або hostname для пінгу.
Наприклад: <code>93.75.123.45</code> або <code>myrouter.ddns.net</code>

⚠️ Потрібна біла (публічна) IP-адреса. Сірі IP (за NAT провайдера) не працюватимуть.`

const msgAddressStepHeartbeat = `<b>Крок 2/3:</b> Введіть адресу вашої локації.
Наприклад: <code>Київ, Хрещатик 1</code>

Або надішліть геопозицію через 📎 → Геопозиція.

<i>📍 Ваша точка буде відображатися на публічній карті. Прибрати її з карти можна в будь-який момент через /info.</i>`

const msgAddressStepPing = `<b>Крок 3/4:</b> Введіть адресу вашої локації.
Наприклад: <code>Київ, Хрещатик 1</code>

Або надішліть геопозицію через 📎 → Геопозиція.

<i>📍 Ваша точка буде відображатися на публічній карті. Прибрати її з карти можна в будь-який момент через /info.</i>`

// ── Ping target validation ──────────────────────────────────────────

const (
	msgPingTargetTooShort = "Занадто коротко. Введіть IP-адресу або hostname."
	msgPingTargetPrivate  = "Ця IP-адреса є приватною (локальною). Потрібна публічна IP-адреса."
)

// ── Address validation ──────────────────────────────────────────────

const (
	msgAddressTooShort  = "Занадто коротко. Введіть адресу, наприклад: <code>Київ, Хрещатик 1</code>"
	msgAddressNotFound  = "Адресу не знайдено. Спробуйте ввести точнішу адресу, наприклад: <code>Київ, вул. Хрещатик, 1</code>"
	msgGeocodeError     = "Не вдалося знайти адресу. Спробуйте ввести інакше або надішліть геопозицію через 📎."
	msgSearchingAddress = "🔍 Шукаю адресу..."
)

// ── Manual address step (after coordinates / GPS) ───────────────────

const msgManualAddressStep = `Координати збережено ✅

Тепер введіть адресу для відображення на карті (вулиця, місто).
Наприклад: <code>Київ, вул. Хрещатик 1</code>`

const msgManualAddressTooShort = "Занадто коротко. Введіть адресу, наприклад: <code>Київ, вул. Хрещатик 1</code>"

// ── Channel step ────────────────────────────────────────────────────

const (
	msgChannelNotAdmin   = "Я не адміністратор цього каналу. Додайте мене як адміна з правом \"Публікація повідомлень\" і спробуйте ще раз."
	msgChannelNoPost     = "У мене немає права \"Публікація повідомлень\" в цьому каналі. Оновіть мої права адміна і спробуйте ще раз."
	msgChannelCheckError = "Не вдалося перевірити мої права в цьому каналі. Переконайтеся, що я доданий як адміністратор."
)

// ── Info detail ─────────────────────────────────────────────────────

const (
	msgInfoTypePing      = "Server Ping"
	msgInfoTypeHeartbeat = "ESP Heartbeat"
	msgInfoPingHint      = "<i>Сервер автоматично пінгує цю адресу кожні 5 хвилин.</i>"
	msgInfoHeartbeatHint = "<i>Налаштуйте ваш пристрій відправляти GET-запити на цей URL кожні 5 хвилин.</i> \n💬 Інструкції з налаштування та допомога: @lights_monitor_chat"
)

// ── Map visibility ───────────────────────────────────────────────────

const (
	msgMapHidden    = "✅ Вашу точку прибрано з публічної карти."
	msgMapShown     = "✅ Вашу точку додано на публічну карту."
	msgMapHideError = "Помилка зміни видимості на карті."
)

// ── /edit ────────────────────────────────────────────────────────────

const msgEditHeader = "<b>Редагування монітора</b>\n\nОберіть монітор для редагування:\n\n"

const (
	msgEditChoose       = "Монітор: <b>%s</b>\n\nЩо бажаєте змінити?"
	msgEditNamePrompt   = "Поточна назва: <b>%s</b>\n\nВведіть нову назву монітора:"
	msgEditAddressPrompt = "Поточна адреса: <b>%s</b>\n\nВведіть нову адресу або надішліть геопозицію через 📎 → Геопозиція."
	msgEditNameTooShort = "Назва занадто коротка. Введіть більш змістовну назву."
	msgEditNameDone     = "✅ Назву оновлено: <b>%s</b>"
	msgEditAddressDone  = "✅ Адресу оновлено: <b>%s</b>"
)

// ── /info list row ───────────────────────────────────────────────────

const msgInfoRow = "<b>%d.</b> %s - %s\n"

// ── /test list row ───────────────────────────────────────────────────

const msgTestRow = "%d. %s (@%s)\n"

// ── Callbacks: stop / resume / delete ────────────────────────────────

const (
	msgStopDone   = "%s <b>%s</b> призупинено.\n\nВідновити можна через /resume"
	msgResumeDone = "%s <b>%s</b> відновлено.\n\nПризупинити можна через /stop"
	msgDeleteDone = "%s <b>%s</b> успішно видалено."
)

// ── Callback: info detail ─────────────────────────────────────────────

const (
	msgInfoDetailHeader   = "<b>📊 Інформація про монітор</b>\n\n"
	msgInfoDetailName     = "🏷 <b>Назва:</b> %s\n"
	msgInfoDetailAddress  = "📍 <b>Адреса:</b> %s\n"
	msgInfoDetailCoords   = "🌐 <b>Координати:</b> %.6f, %.6f\n\n"
	msgInfoDetailStatus   = "<b>Статус:</b> %s\n"
	msgInfoDetailLastPing = "<b>Останній пінг:</b> %s\n"
	msgInfoDetailChannel  = "<b>Канал:</b> @%s\n\n"
	msgInfoDetailTypePing = "<b>🌐 Тип:</b> %s\n"
	msgInfoDetailTarget   = "<b>🎯 Ціль:</b> <code>%s</code>\n\n"
	msgInfoDetailTypeHB   = "<b>📡 Тип:</b> %s\n"
	msgInfoDetailURLLabel = "<b>🔗 URL для пінгу:</b>\n"
	msgInfoDetailURL      = "<code>%s/api/ping/%s</code>\n\n"
)

// ── Buttons ───────────────────────────────────────────────────────────

const (
	msgEditBtnName            = "✏️ Змінити назву"
	msgEditBtnAddress         = "📍 Змінити адресу"
	msgEditBtnRefreshChannel  = "🔄 Оновити тег каналу"
	msgEditBtnShowAddress     = "📍 Показувати адресу в сповіщеннях"
	msgEditBtnHideAddress     = "📍 Приховати адресу в сповіщеннях"
	msgMapBtnHide             = "🗺 Прибрати з карти"
	msgMapBtnShow             = "🗺 Додати на карту"
)

const (
	msgNotifyAddressEnabled  = "✅ Адресу буде показано в сповіщеннях."
	msgNotifyAddressDisabled = "✅ Адресу приховано зі сповіщень."
	msgNotifyAddressError    = "Помилка зміни налаштування."
)

const (
	msgEditChannelRefreshDone     = "✅ Тег каналу оновлено: @%s"
	msgEditChannelRefreshNoChange = "✅ Тег каналу вже актуальний: @%s"
	msgEditChannelRefreshError    = "Не вдалося отримати дані каналу. Спробуйте пізніше."
)

// ── /test notification ────────────────────────────────────────────────

const (
	msgTestNotification = "🧪 <b>Тестове повідомлення</b>\n\nМонітор: <b>%s</b>\nАдреса: %s\n\nЯкщо ви бачите це повідомлення, то налаштування каналу працює коректно! ✅"
	msgTestSentTo       = "%s відправлено в канал <b>@%s</b>"
)

// ── Ping target validation ────────────────────────────────────────────

const (
	msgPingHostNotFound    = "Не вдалося знайти хост <code>%s</code>. Перевірте адресу і спробуйте ще раз."
	msgPingChecking        = "🔍 Перевіряю доступність <code>%s</code>..."
	msgPingHostUnreachable = "❌ Хост <code>%s</code> не відповідає на ICMP ping.\nПереконайтесь, що роутер дозволяє ICMP і спробуйте ще раз."
	msgPingHostOK          = "✅ Хост доступний: <code>%s</code> → <code>%s</code>"
)

// ── Address / geocode ─────────────────────────────────────────────────

const msgAddressFound = "Знайдено: <b>%s</b>"

// ── Channel step ──────────────────────────────────────────────────────

const (
	msgChannelNotFound = "Не вдалося знайти канал <b>%s</b>. Переконайтеся, що канал існує і має публічний username. Спробуйте ще раз."
	msgChannelStep     = `Геопозицію встановлено: <code>%.5f, %.5f</code>

<b>Крок %s:</b> Створіть Telegram-канал і додайте мене як адміністратора з правом "Публікація повідомлень".

Потім надішліть мені @username каналу (напр., @my_power_channel).`
)

// ── Create success ────────────────────────────────────────────────────

const msgCreateDonePing = `<b>Монітор налаштовано!</b>

<b>Назва:</b> %s
<b>Тип:</b> Server Ping
<b>Ціль:</b> <code>%s</code>
<b>Координати:</b> %.5f, %.5f
<b>Канал:</b> @%s

Сервер пінгуватиме <code>%s</code> кожні 5 хвилин.

Коли пінги не проходять — я сповіщу канал, що світла немає. Коли відновляться — що світло повернулося.`

const msgCreateDoneHeartbeat = `<b>Монітор налаштовано!</b>

<b>Назва:</b> %s
<b>Тип:</b> ESP Heartbeat
<b>Координати:</b> %.5f, %.5f
<b>Канал:</b> @%s

<b>Посилання для пінгу:</b>
<code>%s</code>

Налаштуйте ваш пристрій надсилати GET-запит на це посилання кожні 5 хвилин.

Коли пінги зупиняться — я сповіщу канал, що світла немає. Коли відновляться — що світло повернулося.

💬 Інструкції з налаштування та допомога: @lights_monitor_chat`

// ── Notifications ───────────────────────────────────────────────────

const (
	msgNotifyOnline      = "🟢 <b>%s Світло з'явилося</b> \n<i>(не було %s)</i>"
	msgNotifyOffline     = "🔴 <b>%s Світла немає</b>\n<i>(воно було %s)</i>"
	msgNotifyAddressLine = "\n📍 <i>%s</i>"
)

// ── Channel access errors ────────────────────────────────────────────

// msgChannelError is sent to the monitor owner when the bot loses channel access.
// %s = monitor name
const msgChannelError = "⚠️ <b>Монітор призупинено</b>\n\nМонітор <b>%s</b> було призупинено, оскільки бот втратив доступ до каналу (канал видалено, бота видалено або відкликано права).\n\nПереконайтеся, що бот є адміністратором каналу з правом \"Публікація повідомлень\", та відновіть моніторинг через /resume."

// msgChannelPaused is posted to the channel when the owner manually pauses monitoring.
const msgChannelPaused = "⏸ <b>Моніторинг призупинено</b>\n\nВласник тимчасово призупинив оновлення статусу."

// msgChannelPausedBySystem is posted to the channel when the system auto-pauses monitoring.
const msgChannelPausedBySystem = "⚠️ <b>Моніторинг призупинено автоматично</b>\n\nБот втратив доступ до каналу. Власник отримав сповіщення."

// msgChannelResumed is posted to the channel when the owner resumes monitoring.
const msgChannelResumed = "▶️ <b>Моніторинг відновлено</b>\n\nВласник відновив оновлення статусу."
