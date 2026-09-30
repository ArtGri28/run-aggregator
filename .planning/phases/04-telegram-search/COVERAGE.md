# API Coverage — Telegram Bot API (через github.com/go-telegram/bot v1.27.0)

> Full coverage by default. Opt-outs are explicit, reasoned decisions.

Поверхность — методы и возможности Bot API, которые нужны сценарию `/start → Найти забег → фильтры → список → карточка → регистрация` (BOT-01…BOT-09) и алертам администратору (MON-05). Матрица фиксирует, что используется, а что сознательно нет.

| capability | decision | reason |
|---|---|---|
| getUpdates (long polling) | INTEGRATE | |
| getMe (проверка токена при старте) | INTEGRATE | |
| sendMessage | INTEGRATE | |
| editMessageText | INTEGRATE | |
| answerCallbackQuery | INTEGRATE | |
| inline-keyboard-callback-buttons | INTEGRATE | |
| inline-keyboard-url-buttons | INTEGRATE | |
| parse-mode-html | INTEGRATE | |
| link-preview-options | INTEGRATE | |
| allowed-updates (message, callback_query) | INTEGRATE | |
| typed-errors (400, 403, 409, 429 retry_after) | INTEGRATE | |
| maybe-inaccessible-message (callback на старое сообщение) | INTEGRATE | |
| webhooks (setWebhook, deleteWebhook) | OPT-OUT | explicitly out of scope: MVP §8.1 — long polling, входящих портов и домена нет |
| setMyCommands / setMyDescription | OPT-OUT | not needed: `/start` открывает бот по умолчанию, `/runs` — скрытая админ-команда; описание владелец задаёт в BotFather |
| deleteMessage | OPT-OUT | not needed: листание правит одно сообщение (editMessageText) |
| sendPhoto / media / files | OPT-OUT | explicitly out of scope: изображения не копируются (MVP §12.2) |
| reply-keyboards | OPT-OUT | not needed: все экраны на inline-кнопках, город — обычным текстом |
| inline-mode (answerInlineQuery) | OPT-OUT | not needed: поиск только в личном чате |
| group-and-channel-chats (chat_member, my_chat_member) | OPT-OUT | explicitly out of scope: апдейты не из личных чатов игнорируются (privateOnly) |
| getChat / getChatMember / user-profile | OPT-OUT | explicitly out of scope: BOT-08 — о пользователях только Telegram ID и настройки поиска |
| payments / games / stickers / stories / business | OPT-OUT | not needed |
| location / venue | OPT-OUT | not needed yet: город вводится текстом или кнопкой (MVP §8.2) |
