package bot

// Рассылки (Phase 10, 10-UX-CONTRACT.md): мастер администратора B-1…B-7,
// сообщения U-1 и U-2 с отказом U-3, экран «Уведомления» U-5 из профиля (U-4)
// и вопрос в конце анкеты U-6. Отправка — через очередь outbox (notify.go):
// мастер только записывает рассылку и строки получателей. В лог не попадают
// ни текст рассылки, ни идентификаторы получателей.

import (
	"context"
	"html"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/normalize"
	"aggregator/internal/store"
)

const (
	// maxBroadcastText — предел текста рассылки в единицах UTF-16, как в поддержке.
	maxBroadcastText = 2000
	// maxBroadcastHTML — предел текста с разметкой: колонка broadcasts.text.
	maxBroadcastHTML = 4096
	// broadcastWord — слово подтверждения отправки (D-12).
	broadcastWord = "ОТПРАВИТЬ"

	bcAnnounceHead = "📣 <b>Новости «Где старт?»</b>"
	bcInviteHead   = "🎯 <b>Приглашение на старт</b>"
	bcAnnounceStop = "🔕 Не присылать объявления"
	bcInviteStop   = "🔕 Отключить уведомления"
	bcCancelLabel  = "✖️ Отмена"
	bcCancelled    = "Рассылка отменена"
	bcNoCriteria   = "Выберите город или вид спорта"
	bcCityAsk      = "Напишите город проживания получателей"
	bcCityMissing  = "Такого города нет в каталоге — по нему отбирать нельзя"
	bcNeedText     = "Нужен текст"
	bcConfirmHint  = "Чтобы отправить, напишите слово ОТПРАВИТЬ, или нажмите «Отмена»"
	bcNobody       = "Получателей нет — отправлять некому"

	notifyLabel     = "🔔 Уведомления"
	notifyHint      = "Чтобы получать приглашения, укажите в профиле город или виды спорта"
	inviteOnNotice  = "🎯 Приглашения включены"
	inviteOffNotice = "🎯 Приглашения выключены"
	newsOnNotice    = "📣 Новости включены"
	newsOffNotice   = "📣 Новости выключены"
	stopAnnounce    = "🔕 Объявления отключены. Включить снова — «👤 Мой профиль» → «🔔 Уведомления»"
	stopInvite      = "🔕 Приглашения отключены. Включить снова — «👤 Мой профиль» → «🔔 Уведомления»"
	inviteThanks    = thanksNotice + ". Приглашения включены"
)

// Что ждёт мастер от администратора текстом.
const (
	waitCity    = "city"
	waitText    = "text"
	waitConfirm = "confirm"
)

// draft — черновик рассылки администратора до подтверждения или отмены.
//
// Упрощение: черновик хранится в памяти бота; перезапуск бота его стирает, и
// мастер начинается заново — ничего не отправлено, журнал не тронут.
// Хранить в базе — если черновики станут длинными или администраторов много.
type draft struct {
	kind   string // store.BroadcastAnnounce, store.BroadcastInvite или "" — вид не выбран
	filter store.BroadcastFilter
	city   string // город отбора с названием страны — для экранов
	wait   string // waitCity, waitText, waitConfirm или ""
	text   string // текст рассылки с разметкой
}

func (b *Bot) getDraft(id int64) (draft, bool) {
	b.draftMu.Lock()
	defer b.draftMu.Unlock()
	d, ok := b.drafts[id]
	if !ok {
		return draft{}, false
	}
	return *d, true
}

func (b *Bot) putDraft(id int64, d draft) {
	b.draftMu.Lock()
	defer b.draftMu.Unlock()
	if b.drafts == nil {
		b.drafts = map[int64]*draft{}
	}
	b.drafts[id] = &d
}

// dropDraft стирает черновик: отмена, отправка, /start.
func (b *Bot) dropDraft(id int64) {
	b.draftMu.Lock()
	defer b.draftMu.Unlock()
	delete(b.drafts, id)
}

func kindLabel(kind string) string {
	if kind == store.BroadcastInvite {
		return "🎯 Приглашение"
	}
	return "📣 Объявление"
}

// sportNames — виды спорта анкеты без эмодзи, в порядке справочника.
func sportNames(codes []string) string {
	var names []string
	for _, o := range profileSports {
		if slices.Contains(codes, o.Code) {
			_, name, _ := strings.Cut(o.Label, " ")
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// bcStartScreen — B-1.
func bcStartScreen() screen {
	return screen{
		text: "📨 <b>Рассылка</b>\n\n📣 Объявление — новости бота всем пользователям, кроме отказавшихся. Без рекламы и приглашений на старты.\n" +
			"🎯 Приглашение — тем, кто включил уведомления, по городу и видам спорта из профиля.",
		kb: keyboard(
			btn("📣 Объявление", callback{Kind: cbBcKind, Value: "a"}),
			btn("🎯 Приглашение", callback{Kind: cbBcKind, Value: "i"}),
			btn(bcCancelLabel, callback{Kind: cbBcCancel})),
	}
}

// bcFilterScreen — B-2: отбор приглашения и число подходящих сейчас.
func bcFilterScreen(d draft, n int) screen {
	city, sports := "не выбран", "не выбран"
	if d.city != "" {
		city = html.EscapeString(d.city)
	}
	if s := sportNames(d.filter.Sports); s != "" {
		sports = s
	}
	var rows []row
	var r row
	for _, o := range profileSports {
		label := o.Label
		if slices.Contains(d.filter.Sports, o.Code) {
			label += " ✓"
		}
		r.add(label, callback{Kind: cbBcSport, Value: o.Code})
		if len(r) == 2 {
			rows, r = append(rows, r), nil
		}
	}
	if len(r) > 0 {
		rows = append(rows, r)
	}
	rows = append(rows,
		btn("🏠 Город", callback{Kind: cbBcCity}),
		btn("➡️ Дальше", callback{Kind: cbBcNext}),
		btn(bcCancelLabel, callback{Kind: cbBcCancel}))
	return screen{
		text: "🎯 <b>Приглашение: кому</b>\n\n🏠 Город: " + city + "\n🏅 Спорт: " + sports + "\n👥 Подходят сейчас: " + strconv.Itoa(n) +
			"\n\nЗадано и то и другое — получатель должен подходить по обоим.",
		kb: keyboard(rows...),
	}
}

func bcCancelKB() *models.InlineKeyboardMarkup {
	return keyboard(btn(bcCancelLabel, callback{Kind: cbBcCancel}))
}

// bcTextScreen — B-3.
func bcTextScreen(kind string) screen {
	what, tail := "приглашения", ""
	if kind == store.BroadcastAnnounce {
		what, tail = "объявления", "\n📣 В объявлении — только новости бота: без рекламы и приглашений на старты."
	}
	return screen{
		text: "✍️ <b>Текст " + what + "</b>\n\nОтправьте текст одним сообщением — до 2000 символов. Ссылки и форматирование сохранятся." + tail,
		kb:   bcCancelKB(),
	}
}

// broadcastMessage — U-1 (объявление) и U-2 (приглашение). У предпросмотра
// (B-4) кнопка отказа ведёт на b:p и ничего не меняет.
func broadcastMessage(kind, text string, preview bool) screen {
	head, label, stop := bcAnnounceHead, bcAnnounceStop, callback{Kind: cbNotifyStop, Value: "a"}
	if kind == store.BroadcastInvite {
		head, label, stop = bcInviteHead, bcInviteStop, callback{Kind: cbNotifyStop, Value: "i"}
	}
	if preview {
		stop = callback{Kind: cbBcPreview}
	}
	return screen{text: head + "\n\n" + text, kb: keyboard(btn(label, stop))}
}

// bcReviewScreen — B-4, второе сообщение предпросмотра. last — дата прошлого
// объявления ("" — не было).
func bcReviewScreen(d draft, n int, last string) screen {
	var sb strings.Builder
	sb.WriteString("📨 <b>Проверьте рассылку</b>\n\nВид: " + kindLabel(d.kind) + "\n👥 Получателей: " + strconv.Itoa(n))
	if d.kind == store.BroadcastAnnounce {
		if last == "" {
			last = "не было"
		}
		sb.WriteString("\n🕓 Прошлое объявление: " + last)
	} else {
		var who []string
		if d.city != "" {
			who = append(who, html.EscapeString(d.city))
		}
		if s := sportNames(d.filter.Sports); s != "" {
			who = append(who, s)
		}
		sb.WriteString("\n🎯 Кому: " + strings.Join(who, " · "))
	}
	if n == 0 {
		sb.WriteString("\n\n" + bcNobody)
	} else {
		sb.WriteString("\n\nЧтобы отправить, напишите слово " + broadcastWord + ".")
	}
	return screen{text: sb.String(), kb: bcCancelKB()}
}

// bcDoneScreen — B-6, итог рассылки администратору.
func bcDoneScreen(kind string, sent, failed int) screen {
	return screen{text: "📨 <b>Рассылка завершена</b>\n\nВид: " + kindLabel(kind) +
		"\n✅ Доставлено: " + strconv.Itoa(sent) + "\n⚠️ Не доставлено: " + strconv.Itoa(failed)}
}

// onBroadcastCmd — /broadcast: B-1 новым сообщением и новый черновик. Не
// администратор получает тот же ответ, что на неизвестную команду.
func (b *Bot) onBroadcastCmd(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if !b.isAdmin(m.From.ID) {
		b.onText(ctx, api, u)
		return
	}
	b.putDraft(m.From.ID, draft{})
	if err := b.send(ctx, m.Chat.ID, bcStartScreen()); err != nil {
		b.logger().Warn("send broadcast", "kind", kindName(err))
	}
}

// waitFor ставит ожидание текста мастером: прежние режимы ввода (город,
// обращение, ссылка) снимаются.
func (b *Bot) waitFor(ctx context.Context, id int64, d draft, wait string) error {
	if err := b.Store.DropAwaiting(ctx, id); err != nil {
		return err
	}
	d.wait = wait
	b.putDraft(id, d)
	return nil
}

// onBroadcast обрабатывает b:*. Только администратору и только при черновике:
// иначе — «Кнопка устарела». Кнопка предпросмотра ничего не делает.
func (b *Bot) onBroadcast(ctx context.Context, q *models.CallbackQuery, c callback) string {
	id := q.From.ID
	if !b.isAdmin(id) {
		return staleNotice
	}
	if c.Kind == cbBcPreview {
		return ""
	}
	d, ok := b.getDraft(id)
	if !ok {
		return staleNotice
	}
	show := func(sc screen) {
		if err := b.show(ctx, q, sc); err != nil {
			b.logger().Warn("show broadcast", "kind", kindName(err))
		}
	}
	fail := func(err error) string {
		b.logger().Error("broadcast", "err", err)
		return errNotice
	}
	switch c.Kind {
	case cbBcCancel:
		b.dropDraft(id)
		show(screen{text: "📨 Рассылка отменена"})
		return bcCancelled
	case cbBcKind:
		if c.Value == "a" {
			d = draft{kind: store.BroadcastAnnounce}
			if err := b.waitFor(ctx, id, d, waitText); err != nil {
				return fail(err)
			}
			show(bcTextScreen(d.kind))
			return ""
		}
		d = draft{kind: store.BroadcastInvite}
		b.putDraft(id, d)
	case cbBcSport, cbBcCity, cbBcNext:
		if d.kind != store.BroadcastInvite {
			return staleNotice
		}
	}
	switch c.Kind {
	case cbBcSport:
		d.filter.Sports = toggleSport(d.filter.Sports, c.Value)
		d.wait = ""
		b.putDraft(id, d)
	case cbBcCity:
		if err := b.waitFor(ctx, id, d, waitCity); err != nil {
			return fail(err)
		}
		show(screen{text: bcCityAsk, kb: bcCancelKB()})
		return ""
	case cbBcNext:
		if d.filter.Empty() {
			return bcNoCriteria
		}
		if err := b.waitFor(ctx, id, d, waitText); err != nil {
			return fail(err)
		}
		show(bcTextScreen(d.kind))
		return ""
	}
	sc, err := b.filterScreen(ctx, d)
	if err != nil {
		return fail(err)
	}
	show(sc)
	return ""
}

// filterScreen — B-2 с числом подходящих получателей.
func (b *Bot) filterScreen(ctx context.Context, d draft) (screen, error) {
	n, err := b.Store.CountRecipients(ctx, store.BroadcastInvite, d.filter)
	if err != nil {
		return screen{}, err
	}
	return bcFilterScreen(d, n), nil
}

// broadcastText принимает текст администратора, которого ждёт мастер: город
// отбора, текст рассылки либо слово подтверждения; true — сообщение
// обработано и дальше не идёт. Режим ввода, поставленный позже ожидания
// мастера (город в профиле, обращение, ссылка), снимает ожидание мастера:
// при постановке ожидания мастер снимает их, так что действующая отметка —
// более поздняя. Текст, начинающийся с «/», мастеру не достаётся.
func (b *Bot) broadcastText(ctx context.Context, m *models.Message) bool {
	id := m.From.ID
	d, ok := b.getDraft(id)
	if !ok || d.wait == "" || strings.HasPrefix(strings.TrimSpace(m.Text), "/") {
		return false
	}
	other, err := b.Store.Awaiting(ctx, id, b.now())
	if err != nil {
		b.logger().Error("broadcast", "err", err)
		b.bcSay(ctx, m.Chat.ID, screen{text: errNotice, plain: true})
		return true
	}
	if other {
		d.wait = ""
		b.putDraft(id, d)
		return false
	}
	switch d.wait {
	case waitCity:
		b.bcCity(ctx, m, d)
	case waitText:
		b.bcText(ctx, m, d)
	default:
		b.bcConfirm(ctx, m, d)
	}
	return true
}

// broadcastNonText — сообщение без текста, пока мастер ждёт текст: отказ;
// true — сообщение обработано.
func (b *Bot) broadcastNonText(ctx context.Context, m *models.Message) bool {
	if m.From == nil {
		return false
	}
	d, ok := b.getDraft(m.From.ID)
	if !ok || d.wait == "" {
		return false
	}
	if other, err := b.Store.Awaiting(ctx, m.From.ID, b.now()); err != nil || other {
		return false
	}
	notice := bcNeedText
	if d.wait == waitConfirm {
		notice = bcConfirmHint
	}
	b.bcSay(ctx, m.Chat.ID, screen{text: notice, plain: true})
	return true
}

func (b *Bot) bcSay(ctx context.Context, chat int64, sc screen) {
	if err := b.send(ctx, chat, sc); err != nil {
		b.logger().Warn("send broadcast", "kind", kindName(err))
	}
}

// bcCity — город отбора: сверка с каталогом, как в анкете, по странам анкеты
// по порядку; найден — B-2 с городом, нет — отказ и прежний B-2.
func (b *Bot) bcCity(ctx context.Context, m *models.Message, d draft) {
	text := strings.Join(strings.Fields(m.Text), " ")
	var found bool
	if key := normalize.CityKey(text); text != "" && utf8.RuneCountInString(text) <= maxCityInput && validCityKey(key) {
		for _, c := range profileCountries {
			if c.Code == "ZZ" {
				continue
			}
			name, n, err := b.Store.CityName(ctx, c.Code, key, b.today())
			if err != nil {
				b.logger().Error("broadcast", "err", err)
				b.bcSay(ctx, m.Chat.ID, screen{text: errNotice, plain: true})
				return
			}
			if n > 0 {
				_, country, _ := strings.Cut(c.Label, " ")
				d.filter.Country, d.filter.CityKey, d.city, found = c.Code, key, name+", "+country, true
				break
			}
		}
	}
	if found {
		d.wait = ""
		b.putDraft(m.From.ID, d)
	} else {
		b.bcSay(ctx, m.Chat.ID, screen{text: bcCityMissing, plain: true})
	}
	sc, err := b.filterScreen(ctx, d)
	if err != nil {
		b.logger().Error("broadcast", "err", err)
		sc = screen{text: errNotice, plain: true}
	}
	b.bcSay(ctx, m.Chat.ID, sc)
}

// bcText — текст рассылки: проверка длины, разметка из сущностей сообщения,
// предпросмотр (B-4) двумя сообщениями.
//
// Упрощение: текст, у которого вместе с разметкой больше 4096 знаков (длинные
// ссылки), отклоняется тем же ответом о длине; потолок — хранить сущности
// отдельно от текста.
func (b *Bot) bcText(ctx context.Context, m *models.Message, d draft) {
	if strings.TrimSpace(m.Text) == "" {
		b.bcSay(ctx, m.Chat.ID, screen{text: bcNeedText, plain: true})
		return
	}
	n := len(utf16.Encode([]rune(m.Text)))
	text := entitiesHTML(m.Text, m.Entities)
	if n > maxBroadcastText || utf8.RuneCountInString(text) > maxBroadcastHTML {
		b.bcSay(ctx, m.Chat.ID, screen{text: "Слишком длинно: " + strconv.Itoa(n) + " из 2000. Отправьте короче", plain: true})
		return
	}
	d.text, d.wait = text, waitConfirm
	b.putDraft(m.From.ID, d)
	cnt, err := b.Store.CountRecipients(ctx, d.kind, d.filter)
	var last string
	if err == nil && d.kind == store.BroadcastAnnounce {
		var t time.Time
		var ok bool
		if t, ok, err = b.Store.LastAnnounce(ctx); ok {
			last = t.In(b.loc).Format("02.01.2006")
		}
	}
	if err != nil {
		b.logger().Error("broadcast", "err", err)
		b.bcSay(ctx, m.Chat.ID, screen{text: errNotice, plain: true})
		return
	}
	b.bcSay(ctx, m.Chat.ID, broadcastMessage(d.kind, text, true))
	b.bcSay(ctx, m.Chat.ID, bcReviewScreen(d, cnt, last))
}

// bcConfirm — слово подтверждения: рассылка и строки очереди записываются, черновик
// стирается (B-5); иной текст — подсказка.
func (b *Bot) bcConfirm(ctx context.Context, m *models.Message, d draft) {
	if !strings.EqualFold(strings.TrimSpace(m.Text), broadcastWord) {
		b.bcSay(ctx, m.Chat.ID, screen{text: bcConfirmHint, plain: true})
		return
	}
	_, n, err := b.Store.CreateBroadcast(ctx, d.kind, m.From.ID, d.filter, d.text, b.now())
	if err != nil {
		b.logger().Error("broadcast", "err", err)
		b.bcSay(ctx, m.Chat.ID, screen{text: errNotice, plain: true})
		return
	}
	b.dropDraft(m.From.ID)
	if n == 0 {
		b.bcSay(ctx, m.Chat.ID, screen{text: bcNobody, plain: true})
		return
	}
	b.logger().Info("broadcast", "event", "queued", "kind", d.kind, "recipients", n)
	b.bcSay(ctx, m.Chat.ID, screen{text: "📨 Рассылка в очереди: " + strconv.Itoa(n) + " получателей. Итог придёт сообщением."})
}

// entitiesHTML — текст сообщения с разметкой Telegram (жирный, курсив,
// подчёркнутый, зачёркнутый, скрытый, код, блок кода, цитата, ссылка с
// текстом) в HTML для parse_mode HTML; прочие сущности и сам текст
// экранируются. Сущности вложены и не пересекаются частично (правило Bot API);
// сущность за пределами текста пропускается. Смещения — в единицах UTF-16.
func entitiesHTML(text string, ents []models.MessageEntity) string {
	units := utf16.Encode([]rune(text))
	var es []models.MessageEntity
	for _, e := range ents {
		if _, _, ok := entityTags(e); ok && e.Offset >= 0 && e.Length > 0 && e.Offset+e.Length <= len(units) {
			es = append(es, e)
		}
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Offset != es[j].Offset {
			return es[i].Offset < es[j].Offset
		}
		return es[i].Length > es[j].Length
	})
	var sb strings.Builder
	var stack []models.MessageEntity
	pos, next := 0, 0
	for {
		for len(stack) > 0 && stack[len(stack)-1].Offset+stack[len(stack)-1].Length <= pos {
			_, closeTag, _ := entityTags(stack[len(stack)-1])
			sb.WriteString(closeTag)
			stack = stack[:len(stack)-1]
		}
		if next < len(es) && es[next].Offset <= pos {
			e := es[next]
			next++
			if len(stack) > 0 && e.Offset+e.Length > stack[len(stack)-1].Offset+stack[len(stack)-1].Length {
				continue // частичное пересечение: сущность пропускается
			}
			open, _, _ := entityTags(e)
			sb.WriteString(open)
			stack = append(stack, e)
			continue
		}
		if pos >= len(units) {
			break
		}
		to := len(units)
		if len(stack) > 0 {
			to = min(to, stack[len(stack)-1].Offset+stack[len(stack)-1].Length)
		}
		if next < len(es) {
			to = min(to, es[next].Offset)
		}
		sb.WriteString(html.EscapeString(string(utf16.Decode(units[pos:to]))))
		pos = to
	}
	return sb.String()
}

// entityTags — открывающий и закрывающий тег сущности; ok = false — сущность
// без разметки (упоминание, хештег, простая ссылка и т. п.).
func entityTags(e models.MessageEntity) (open, close string, ok bool) {
	tag := map[models.MessageEntityType]string{
		models.MessageEntityTypeBold: "b", models.MessageEntityTypeItalic: "i", models.MessageEntityTypeUnderline: "u",
		models.MessageEntityTypeStrikethrough: "s", models.MessageEntityTypeSpoiler: "tg-spoiler", models.MessageEntityTypeCode: "code",
		models.MessageEntityTypePre: "pre", models.MessageEntityTypeBlockquote: "blockquote",
	}[e.Type]
	switch {
	case e.Type == models.MessageEntityTypeTextLink && e.URL != "":
		return `<a href="` + html.EscapeString(e.URL) + `">`, "</a>", true
	case tag != "":
		return "<" + tag + ">", "</" + tag + ">", true
	}
	return "", "", false
}

// notifyScreen — U-5 «Уведомления».
func notifyScreen(p store.NotifyPrefs) screen {
	invite, news := "выключены", "включены"
	if p.Invite {
		invite = "включены"
	}
	if p.AnnounceOff {
		news = "выключены"
	}
	text := "🔔 <b>Уведомления</b>\n\n🎯 Приглашения на старты: " + invite +
		"\nПриходят от администратора бота — по вашему городу и видам спорта из профиля."
	var rows []row
	switch {
	case p.Invite:
		rows = append(rows, btn("🎯 Выключить приглашения", callback{Kind: cbNotifyInvite, Value: "0"}))
	case p.Eligible:
		rows = append(rows, btn("🎯 Включить приглашения", callback{Kind: cbNotifyInvite, Value: "1"}))
	default:
		text += "\n" + notifyHint
	}
	text += "\n\n📣 Новости бота: " + news
	if p.AnnounceOff {
		rows = append(rows, btn("📣 Включить новости", callback{Kind: cbNotifyNews, Value: "1"}))
	} else {
		rows = append(rows, btn("📣 Выключить новости", callback{Kind: cbNotifyNews, Value: "0"}))
	}
	rows = append(rows, btn(backLabel, callback{Kind: cbProfile}))
	return screen{text: text, kb: keyboard(rows...)}
}

// onNotify обрабатывает n:*: экран «Уведомления», его переключатели и отказ из
// сообщения рассылки. Версии профиля переключатели не меняют. Идентификатор
// пользователя — только q.From.ID.
func (b *Bot) onNotify(ctx context.Context, q *models.CallbackQuery, c callback) string {
	id, now := q.From.ID, b.now()
	var notice string
	var err error
	switch c.Kind {
	case cbNotifyStop:
		notice = stopAnnounce
		if c.Value == "i" {
			notice = stopInvite
			_, err = b.Store.SetInvite(ctx, id, false, now)
		} else {
			err = b.Store.SetAnnounceOff(ctx, id, true, now)
		}
		if err != nil {
			b.logger().Error("notify prefs", "err", err)
			return errNotice
		}
		b.logger().Info("notify prefs", "action", "stop", "kind", c.Value)
		if m := q.Message.Message; m != nil && m.ReplyMarkup != nil {
			rows := slices.DeleteFunc(slices.Clone(m.ReplyMarkup.InlineKeyboard), func(r []models.InlineKeyboardButton) bool {
				return slices.ContainsFunc(r, func(bt models.InlineKeyboardButton) bool { return strings.HasPrefix(bt.CallbackData, "n:x:") })
			})
			if err := b.showKeyboard(ctx, m.Chat.ID, m.ID, &models.InlineKeyboardMarkup{InlineKeyboard: rows}); err != nil {
				b.logger().Warn("edit broadcast", "err", err)
			}
		}
		return notice
	case cbNotifyInvite:
		var ok bool
		ok, err = b.Store.SetInvite(ctx, id, c.Value == "1", now)
		if err == nil && !ok && c.Value == "1" {
			return staleNotice
		}
		notice = inviteOffNotice
		if c.Value == "1" {
			notice = inviteOnNotice
		}
	case cbNotifyNews:
		err = b.Store.SetAnnounceOff(ctx, id, c.Value == "0", now)
		notice = newsOnNotice
		if c.Value == "0" {
			notice = newsOffNotice
		}
	}
	if err != nil {
		b.logger().Error("notify prefs", "err", err)
		return errNotice
	}
	p, err := b.Store.NotifyPrefs(ctx, id)
	if err != nil {
		b.logger().Error("notify prefs", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, notifyScreen(p)); err != nil {
		b.logger().Warn("show notify", "err", err)
	}
	return notice
}

// inviteQuestion — U-6, вопрос в конце анкеты; кнопки несут версию профиля.
func inviteQuestion(pv int) screen {
	return screen{
		text: "<b>Последний вопрос</b>\nПрисылать приглашения на старты по вашему городу и видам спорта? Отключить можно в любой момент.",
		kb: keyboard(
			btn("🔔 Да, присылать", callback{Kind: cbProfInvite, Value: "1", V: pv}),
			btn("Нет, спасибо", callback{Kind: cbProfInvite, Value: "0", V: pv})),
	}
}

// profileInvite — ответ на U-6: при актуальной версии и завершённой анкете
// согласие записывается, дальше — главное меню и благодарность; иначе —
// «Кнопка устарела» без записи.
func (b *Bot) profileInvite(ctx context.Context, q *models.CallbackQuery, c callback) string {
	err := b.Store.AnswerInviteAt(ctx, q.From.ID, c.V, c.Value == "1", b.now())
	if err == store.ErrStaleProfile {
		return staleNotice
	}
	if err != nil {
		b.logger().Error("profile", "err", err)
		return errNotice
	}
	if n := b.onMenu(ctx, q, callback{}); n != "" {
		return n
	}
	if c.Value == "1" {
		return inviteThanks
	}
	return thanksNotice
}
