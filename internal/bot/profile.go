package bot

import (
	"context"
	"errors"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
)

// Анкета и профиль (Phase 9, 09-UX-CONTRACT.md): предложение на /start,
// вопросы O-2…O-5, завершение O-6, экран профиля P-1 с правкой ответов P-2 и
// безопасная обработка устаревших кнопок u:.
// Профиль живёт в своей таблице со своей версией (profile_v) и от настроек
// поиска не зависит (D-08): код этого файла не читает и не пишет фильтры и
// строку users, а версия кнопок u: ставится при построении экрана, не stamp.

// Режим экрана вопроса: прохождение анкеты или правка ответа.
const (
	modeWizard = "w"
	modeEdit   = "e"
)

const (
	// awaitCityFor — сколько после показа экрана города текст считается его названием.
	awaitCityFor = 10 * time.Minute
	// thanksNotice — уведомление на «✅ Готово».
	thanksNotice = "Спасибо! Анкета сохранена"
	// maxProfileCities — сколько городов каталога показывается кнопками.
	maxProfileCities = 10
)

// Поля анкеты в порядке вопросов; для каждого — какой экран показать следом
// после ответа. profileEditNext — то же при правке: пустое значение — экран
// профиля P-1.
var (
	profileNext     = map[string]string{"g": "a", "a": "k", "k": "c", "c": "s", "s": "s"}
	profileEditNext = map[string]string{"g": "", "a": "", "k": "c", "c": "", "s": "s"}
)

// offerScreen — O-1: предложение анкеты вместо главного меню на первом /start.
func offerScreen(pv int) screen {
	return screen{
		text: "🏁 <b>Где старт?</b>\nЛюбительские старты в разных городах\n\n" +
			"Чтобы перейти в приложение, нажмите кнопку «Каталог».\n\n" +
			"Можно ответить на 4 коротких вопроса о себе. " +
			"Анкета добровольная: любой вопрос можно пропустить, а ответы — изменить в /profile. Поиск работает и без неё.",
		kb: keyboard(
			btn("📝 Заполнить анкету", callback{Kind: cbProfStart, V: pv}),
			btn("Пропустить", callback{Kind: cbProfSkip, V: pv})),
	}
}

// profileLabel — подпись значения из справочника; неизвестный код — он сам.
func profileLabel(opts []profileOption, code string) string {
	for _, o := range opts {
		if o.Code == code {
			return o.Label
		}
	}
	return code
}

// questionTitle — номер вопроса анкеты и его заголовок.
func questionTitle(field string) (n int, title string) {
	switch field {
	case "g":
		return 1, "Ваш пол"
	case "a":
		return 2, "Ваш возраст"
	case "s":
		return 4, "Какие виды спорта вам близки? (можно несколько)"
	}
	return 3, "Где вы живёте?"
}

// questionScreen — вопросы O-2…O-5 и их правка (режим e): чистая функция без
// обращений к базе. cities — кнопки городов каталога страны профиля (только
// для поля c). Все кнопки несут версию профиля pv; «⬅️ Назад» — одна и
// последняя.
func questionScreen(field, mode string, p store.Profile, pv int, cities []store.CityCount) screen {
	n, title := questionTitle(field)
	wizard := mode == modeWizard
	head := "<b>" + title + "</b>"
	if wizard {
		head = "<b>Вопрос " + strconv.Itoa(n) + " из 4</b>"
		if field != "c" {
			head += "\n" + title
		}
	}
	ask := func(f string) callback { return callback{Kind: cbProfAsk, Field: f, Mode: mode, V: pv} }
	pick := func(code string) callback {
		return callback{Kind: cbProfSave, Field: field, Value: code, Mode: mode, V: pv}
	}

	var rows []row
	var r row
	add := func(label string, c callback) {
		r.add(label, c)
		if len(r) == 2 {
			rows, r = append(rows, r), nil
		}
	}
	options := func(opts []profileOption, on func(code string) bool) {
		for _, o := range opts {
			add(check(on(o.Code), o.Label), pick(o.Code))
		}
	}
	text := head
	switch field {
	case "g":
		options(profileGenders, func(code string) bool { return p.Gender == code })
	case "a":
		options(profileAges, func(code string) bool { return p.AgeGroup >= 0 && strconv.Itoa(p.AgeGroup) == code })
	case "k":
		options(profileCountries, func(code string) bool { return p.Country == code })
	case "c":
		tail := "Напишите название города сообщением"
		if len(cities) > 0 {
			tail = "Выберите город или напишите его название сообщением"
		}
		text += "\nСтрана: " + profileLabel(profileCountries, p.Country) + "\n\n" + tail
		for _, c := range cities {
			add(c.Name, pick(c.Key))
		}
	case "s":
		options(profileSports, func(code string) bool { return slices.Contains(p.Sports, code) })
	}
	if len(r) > 0 {
		rows = append(rows, r)
	}

	if wizard {
		if field != "s" {
			skip := profileNext[field]
			if field == "k" {
				skip = "s" // страна пропускается вместе с городом
			}
			rows = append(rows, btn("Пропустить вопрос", ask(skip)))
		} else {
			rows = append(rows, btn("✅ Готово", callback{Kind: cbProfDone, V: pv}))
		}
	}
	rows = append(rows, btn(backLabel, questionBack(field, mode, pv)))
	return screen{text: text, kb: keyboard(rows...)}
}

// questionBack — куда ведёт «⬅️ Назад» с вопроса: на предыдущий вопрос, с
// первого — в главное меню; в режиме правки — в профиль, с города — на страну.
func questionBack(field, mode string, pv int) callback {
	if mode == modeEdit {
		if field == "c" {
			return callback{Kind: cbProfAsk, Field: "k", Mode: mode, V: pv}
		}
		return callback{Kind: cbProfile}
	}
	prev := map[string]string{"a": "g", "k": "a", "c": "k", "s": "k"}[field]
	if prev == "" {
		return callback{Kind: cbMenu}
	}
	return callback{Kind: cbProfAsk, Field: prev, Mode: mode, V: pv}
}

// questionFor строит экран вопроса; для города читает кнопки из каталога
// страны профиля («Другая страна» и страна без городов — без кнопок).
func (b *Bot) questionFor(ctx context.Context, field, mode string, p store.Profile, pv int) (screen, error) {
	var cities []store.CityCount
	if field == "c" && p.Country != "" && p.Country != "ZZ" {
		var err error
		cities, err = b.Store.TopCities(ctx, event.Filter{From: b.today(), CountryCode: p.Country}, maxProfileCities)
		if err != nil {
			return screen{}, err
		}
	}
	return questionScreen(field, mode, p, pv, cities), nil
}

// navTarget — какой экран показывает кнопка u:a при текущем профиле: режим
// анкеты только при статусе in_progress, иначе правка; город без выбранной
// страны — экран страны.
func navTarget(p store.Profile, c callback) (field, mode string) {
	field, mode = c.Field, c.Mode
	if mode == modeWizard && p.Status != store.ProfileInProgress {
		mode = modeEdit
	}
	if field == "c" && p.Country == "" {
		field = "k"
	}
	return field, mode
}

// withAwait ставит отметку ожидания города (показан экран города) или снимает
// её (показан любой другой экран). Версию профиля отметка не меняет.
func (b *Bot) withAwait(p store.Profile, field, mode string) store.Profile {
	if field == "c" {
		p.AwaitingCityUntil, p.AwaitingCityMode = b.now().Add(awaitCityFor), mode
	} else {
		p.AwaitingCityUntil, p.AwaitingCityMode = time.Time{}, ""
	}
	return p
}

// toggleSport добавляет или снимает вид спорта; порядок — как в справочнике.
func toggleSport(cur []string, code string) []string {
	var out []string
	for _, o := range profileSports {
		on := slices.Contains(cur, o.Code)
		if o.Code == code {
			on = !on
		}
		if on {
			out = append(out, o.Code)
		}
	}
	return out
}

// onProfile обрабатывает u:*. Изменяющая кнопка применяется только при
// актуальной версии профиля и допустимом статусе (проверка — внутри
// UpdateProfileAt, под блокировкой строки); иначе — «Кнопка устарела» без
// записи. Навигационная кнопка устаревшей версии показывает экран по текущему
// состоянию, тоже без записи. Идентификатор пользователя — только q.From.ID.
func (b *Bot) onProfile(ctx context.Context, q *models.CallbackQuery, c callback) string {
	switch {
	case c.Kind == cbProfile:
		return b.profileShow(ctx, q)
	case c.Kind == cbProfAsk:
		return b.profileAsk(ctx, q, c)
	}
	return b.profileWrite(ctx, q, c)
}

// profileScreen — P-1 «Мой профиль»: чистая функция над ответами. Пустые
// значения — «не указан» и «не указано»; город экранируется. Кнопки полей
// несут версию профиля pv.
func profileScreen(p store.Profile, pv int) screen {
	gender, age, home, sports := "не указан", "не указан", "не указано", "не указан"
	if p.Gender != "" {
		gender = profileLabel(profileGenders, p.Gender)
	}
	if p.AgeGroup >= 0 {
		age = profileLabel(profileAges, strconv.Itoa(p.AgeGroup))
	}
	if p.Country != "" {
		home = profileLabel(profileCountries, p.Country)
		if p.City != "" {
			home += ", " + html.EscapeString(p.City)
		}
	}
	var names []string
	for _, o := range profileSports {
		if slices.Contains(p.Sports, o.Code) {
			_, name, _ := strings.Cut(o.Label, " ") // подпись без эмодзи
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		sports = strings.Join(names, ", ")
	}
	ask := func(f string) callback { return callback{Kind: cbProfAsk, Field: f, Mode: modeEdit, V: pv} }
	var r1, r2 row
	r1.add("🚻 Пол", ask("g"))
	r1.add("🎂 Возраст", ask("a"))
	r2.add("🏠 Где живу", ask("k"))
	r2.add("🏅 Спорт", ask("s"))
	return screen{
		text: "👤 <b>Мой профиль</b>\n\n🚻 Пол: " + gender + "\n🎂 Возраст: " + age + "\n🏠 Живу: " + home +
			"\n🏅 Спорт: " + sports,
		kb: keyboard(r1, r2, btn(backLabel, callback{Kind: cbMenu})),
	}
}

// profileOpen читает профиль для экрана P-1 без записи в users; стоящую
// отметку ожидания города снимает (версия профиля при этом не меняется).
// Нет строки профиля — пустой профиль и версия 0, ничего не создаётся.
func (b *Bot) profileOpen(ctx context.Context, tgID int64) (store.Profile, int, error) {
	p, v, ok, err := b.Store.PeekProfile(ctx, tgID)
	if err != nil || !ok || p.AwaitingCityUntil.IsZero() {
		return p, v, err
	}
	np, nv, err := b.Store.UpdateProfileAt(ctx, tgID, v, b.now(), func(cur store.Profile) (store.Profile, error) {
		cur.AwaitingCityUntil, cur.AwaitingCityMode = time.Time{}, ""
		return cur, nil
	})
	if errors.Is(err, store.ErrStaleProfile) { // профиль изменили между чтением и записью
		p, v, _, err = b.Store.PeekProfile(ctx, tgID)
		return p, v, err
	}
	return np, nv, err
}

// onProfileCmd — /profile: P-1 новым сообщением.
func (b *Bot) onProfileCmd(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	sc := screen{text: errNotice, plain: true}
	p, v, err := b.profileOpen(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("profile", "err", err)
	} else {
		sc = profileScreen(p, v)
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send profile", "err", err)
	}
}

// profileShow — u:o: P-1 правкой сообщения.
func (b *Bot) profileShow(ctx context.Context, q *models.CallbackQuery) string {
	p, v, err := b.profileOpen(ctx, q.From.ID)
	if err != nil {
		b.logger().Error("profile", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, profileScreen(p, v)); err != nil {
		b.logger().Warn("show profile", "err", err)
	}
	return ""
}

// profileAsk — u:a: показать вопрос. Актуальная кнопка ставит или снимает
// отметку ожидания города (версия не меняется), устаревшая — ничего не пишет.
func (b *Bot) profileAsk(ctx context.Context, q *models.CallbackQuery, c callback) string {
	var field, mode string
	p, v, err := b.Store.UpdateProfileAt(ctx, q.From.ID, c.V, b.now(), func(cur store.Profile) (store.Profile, error) {
		field, mode = navTarget(cur, c)
		return b.withAwait(cur, field, mode), nil
	})
	if errors.Is(err, store.ErrStaleProfile) {
		p, v, _, err = b.Store.PeekProfile(ctx, q.From.ID)
		field, mode = navTarget(p, c)
	}
	if err != nil {
		b.logger().Error("profile", "err", err)
		return errNotice
	}
	return b.showQuestion(ctx, q, field, mode, p, v)
}

func (b *Bot) showQuestion(ctx context.Context, q *models.CallbackQuery, field, mode string, p store.Profile, v int) string {
	sc, err := b.questionFor(ctx, field, mode, p, v)
	if err != nil {
		b.logger().Error("profile screen", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show profile", "err", err)
	}
	return ""
}

// profileWrite — u:b, u:n, u:z, u:s: изменение профиля.
func (b *Bot) profileWrite(ctx context.Context, q *models.CallbackQuery, c callback) string {
	field, mode := "g", modeWizard // экран после записи: вопрос 1 после «Заполнить анкету»
	if c.Kind == cbProfSave {
		field, mode = profileNext[c.Field], c.Mode
		if mode == modeEdit {
			field = profileEditNext[c.Field]
		}
	}
	p, v, err := b.Store.UpdateProfileAt(ctx, q.From.ID, c.V, b.now(), func(cur store.Profile) (store.Profile, error) {
		next, err := b.applyProfile(ctx, cur, c)
		if err != nil {
			return store.Profile{}, err
		}
		return b.withAwait(next, field, mode), nil
	})
	if errors.Is(err, store.ErrStaleProfile) {
		return staleNotice
	}
	if err != nil {
		b.logger().Error("profile", "err", err)
		return errNotice
	}
	switch c.Kind {
	case cbProfSkip:
		return b.onMenu(ctx, q, callback{})
	case cbProfDone:
		if n := b.onMenu(ctx, q, callback{}); n != "" {
			return n
		}
		return thanksNotice
	}
	if field == "" { // правка закончена: вернуться в профиль
		if err := b.show(ctx, q, profileScreen(p, v)); err != nil {
			b.logger().Warn("show profile", "err", err)
		}
		return ""
	}
	return b.showQuestion(ctx, q, field, mode, p, v)
}

// editStatus — статус после ответа через профиль: из not_started и skipped —
// in_progress; пол, возраст, страна и хотя бы один вид спорта — completed;
// completed не меняется.
func editStatus(p store.Profile) string {
	switch {
	case p.Status == store.ProfileCompleted:
		return p.Status
	case p.Gender != "" && p.AgeGroup >= 0 && p.Country != "" && len(p.Sports) > 0:
		return store.ProfileCompleted
	}
	return store.ProfileInProgress
}

// applyProfile — новое состояние профиля после кнопки c; недопустимый для
// статуса переход или значение, которого нет в каталоге, — ErrStaleProfile.
func (b *Bot) applyProfile(ctx context.Context, cur store.Profile, c callback) (store.Profile, error) {
	next := cur
	switch c.Kind {
	case cbProfStart:
		if cur.Status != store.ProfileNotStarted {
			return cur, store.ErrStaleProfile
		}
		next.Status = store.ProfileInProgress
	case cbProfSkip:
		if cur.Status != store.ProfileNotStarted {
			return cur, store.ErrStaleProfile
		}
		next.Status = store.ProfileSkipped
	case cbProfDone:
		if cur.Status != store.ProfileInProgress {
			return cur, store.ErrStaleProfile
		}
		next.Status = store.ProfileCompleted
	case cbProfSave:
		if c.Mode == modeWizard && cur.Status != store.ProfileInProgress {
			return cur, store.ErrStaleProfile
		}
		switch c.Field {
		case "g":
			next.Gender = c.Value
		case "a":
			next.AgeGroup, _ = strconv.Atoi(c.Value)
		case "k":
			if c.Value != cur.Country {
				next.City, next.CityKey = "", ""
			}
			next.Country = c.Value
		case "c":
			if cur.Country == "" {
				return cur, store.ErrStaleProfile
			}
			name, n, err := b.Store.CityName(ctx, cur.Country, c.Value, b.today())
			if err != nil {
				return cur, err
			}
			if n == 0 {
				return cur, store.ErrStaleProfile // ключа нет в каталоге страны
			}
			next.City, next.CityKey = name, c.Value
		case "s":
			next.Sports = toggleSport(cur.Sports, c.Value)
		}
		if c.Mode == modeEdit {
			next.Status = editStatus(next)
		}
	}
	return next, nil
}

// cityAckScreen — ответ на принятый текст города: «Дальше» (вопрос 4 в анкете,
// профиль при правке) и «Изменить» (экран города того же режима). Город
// экранируется, в callback_data не попадает.
func cityAckScreen(city, mode string, pv int) screen {
	next := callback{Kind: cbProfile}
	if mode != modeEdit {
		mode = modeWizard
		next = callback{Kind: cbProfAsk, Field: "s", Mode: mode, V: pv}
	}
	var r row
	r.add("Дальше", next)
	r.add("Изменить", callback{Kind: cbProfAsk, Field: "c", Mode: mode, V: pv})
	return screen{text: "🏠 Город проживания: " + html.EscapeString(city), kb: keyboard(r)}
}

// residenceText принимает текст как город проживания, если стоит действующая
// отметка ожидания (D-10); true — текст принят (или обработан ошибкой) и в
// поиск не идёт. Текст — уже одна строка до 64 символов без «/». Без отметки,
// после истечения окна и при отметке, снятой между чтением и записью, —
// false без записи. users не пишется, фильтры не читаются; текст города в лог
// не попадает (BOT-08, D-09).
func (b *Bot) residenceText(ctx context.Context, m *models.Message, text string) bool {
	p, v, ok, err := b.Store.PeekProfile(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("residence", "err", err)
		return false
	}
	live := func(p store.Profile) bool { return !p.AwaitingCityUntil.IsZero() && p.AwaitingCityUntil.After(b.now()) }
	if !ok || !live(p) {
		return false
	}
	// Совпавший с каталогом города страны текст сохраняется под названием и
	// ключом каталога; иначе — как введён с пустым ключом. «Другая страна» без
	// каталога.
	city, key := text, ""
	if p.Country != "ZZ" {
		if k := normalize.CityKey(text); validCityKey(k) {
			name, n, err := b.Store.CityName(ctx, p.Country, k, b.today())
			if err != nil {
				b.logger().Error("residence", "err", err)
				return false
			}
			if n > 0 {
				city, key = name, k
			}
		}
	}
	var mode string
	np, nv, err := b.Store.UpdateProfileAt(ctx, m.From.ID, v, b.now(), func(cur store.Profile) (store.Profile, error) {
		if !live(cur) { // отметку сняли или она истекла, пока ждали блокировку строки
			return cur, store.ErrStaleProfile
		}
		mode = cur.AwaitingCityMode
		cur.City, cur.CityKey = city, key
		cur.AwaitingCityUntil, cur.AwaitingCityMode = time.Time{}, ""
		if mode == modeEdit {
			cur.Status = editStatus(cur)
		}
		return cur, nil
	})
	if errors.Is(err, store.ErrStaleProfile) {
		return false
	}
	sc := cityAckScreen(np.City, mode, nv)
	if err != nil {
		b.logger().Error("residence", "err", err)
		sc = screen{text: errNotice, plain: true}
	} else {
		b.logger().Info("text", "residence", true)
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send residence", "err", err)
	}
	return true
}
