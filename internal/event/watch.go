package event

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

// maxURLLen — предел Telegram для URL-кнопки.
const maxURLLen = 2048

// ValidURL — ссылка, которую можно показать: http/https с хостом, без
// пробелов и управляющих символов, не длиннее maxURLLen. Одна проверка для
// кнопки «Регистрация» в карточке и для сообщения «появилась ссылка» (D-05).
func ValidURL(s string) bool {
	if s == "" || len(s) > maxURLLen || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

// DateTBD — у старта дата-заглушка: в названии есть слова «дата уточняется»
// без учёта регистра, как их ставит адаптер Tristyle. Единственная реализация
// признака в проекте.
//
// Упрощение: два потолка. Источник, помечающий заглушку иначе, не
// распознаётся. Название, в котором организатор сам написал «дата уточняется»
// при точной дате, считается заглушкой: напоминания о таком старте нет, дата в
// сообщении стоит с пометкой. Точнее — признаком в схеме события.
func DateTBD(title string) bool {
	return strings.Contains(strings.ToLower(title), "дата уточняется")
}

// WatchState — состояние старта, за которым следит избранное.
type WatchState struct {
	Title     string
	StartDate time.Time
	Status    Status
	URL       string
}

// DateChange — прежняя и новая дата старта с признаками даты-заглушки.
type DateChange struct {
	From, To       time.Time
	FromTBD, ToTBD bool
}

// StatusChange — прежний и новый статус старта.
type StatusChange struct {
	From, To Status
}

// Change — что изменилось у старта за один прогон источника. Gone — старт
// пропал из календаря (D-17): частей о полях тогда нет.
type Change struct {
	EventID int64
	Gone    bool
	// Link — годной ссылки не было, появилась (D-05).
	Link   bool
	Date   *DateChange
	Status *StatusChange
}

// Виды строки очереди сообщений о старте.
const (
	KindChange = "change"
	KindGone   = "gone"
)

// Kind — вид строки очереди для изменения.
func (c Change) Kind() string {
	if c.Gone {
		return KindGone
	}
	return KindChange
}

// changePayload — содержимое строки очереди: свои короткие ключи, а не имена
// полей Go; незнакомые ключи при чтении игнорируются.
type changePayload struct {
	Link   bool           `json:"link,omitempty"`
	Date   *payloadDate   `json:"date,omitempty"`
	Status *payloadStatus `json:"status,omitempty"`
}

type payloadDate struct {
	From    string `json:"from"`
	To      string `json:"to"`
	FromTBD bool   `json:"from_tbd,omitempty"`
	ToTBD   bool   `json:"to_tbd,omitempty"`
}

type payloadStatus struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Payload — содержимое строки очереди: что изменилось, без текста сообщения.
// У изменения «пропал» — пустой объект.
func (c Change) Payload() []byte {
	var p changePayload
	if !c.Gone {
		p.Link = c.Link
		if d := c.Date; d != nil {
			p.Date = &payloadDate{d.From.Format(time.DateOnly), d.To.Format(time.DateOnly), d.FromTBD, d.ToTBD}
		}
		if st := c.Status; st != nil {
			p.Status = &payloadStatus{string(st.From), string(st.To)}
		}
	}
	// Marshal структуры из строк и признаков не возвращает ошибку.
	b, _ := json.Marshal(p)
	return b
}

// ParseChange — обратное Kind и Payload: изменение по виду, событию и
// содержимому строки очереди.
func ParseChange(kind string, eventID int64, payload []byte) (Change, error) {
	c := Change{EventID: eventID}
	switch kind {
	case KindGone:
		c.Gone = true
		return c, nil
	case KindChange:
	default:
		return Change{}, fmt.Errorf("вид строки очереди %q", kind)
	}
	var p changePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Change{}, fmt.Errorf("содержимое строки очереди: %w", err)
	}
	c.Link = p.Link
	if p.Date != nil {
		from, err := time.Parse(time.DateOnly, p.Date.From)
		if err != nil {
			return Change{}, fmt.Errorf("содержимое строки очереди: %w", err)
		}
		to, err := time.Parse(time.DateOnly, p.Date.To)
		if err != nil {
			return Change{}, fmt.Errorf("содержимое строки очереди: %w", err)
		}
		c.Date = &DateChange{From: from, To: to, FromTBD: p.Date.FromTBD, ToTBD: p.Date.ToTBD}
	}
	if p.Status != nil {
		c.Status = &StatusChange{From: Status(p.Status.From), To: Status(p.Status.To)}
	}
	return c, nil
}

// WatchDiff сравнивает состояние стартов до и после применения прогона и
// возвращает изменения в порядке идентификаторов — не больше одного на старт.
// Старт из обоих снимков: появилась годная ссылка (замена и исчезновение
// ссылки не считаются); изменилась дата либо признак даты-заглушки; изменился
// статус. Старт только из снимка «до», названный в hidden, — «пропал»; не
// названный (слит, прошёл) — ничего. Старт только из снимка «после» (новый,
// вернувшийся) — ничего. Название само по себе изменением не считается.
func WatchDiff(before, after map[int64]WatchState, hidden []int64) []Change {
	var out []Change
	for id, b := range before {
		a, ok := after[id]
		if !ok {
			if slices.Contains(hidden, id) {
				out = append(out, Change{EventID: id, Gone: true})
			}
			continue
		}
		c := Change{EventID: id, Link: !ValidURL(b.URL) && ValidURL(a.URL)}
		if bt, at := DateTBD(b.Title), DateTBD(a.Title); !b.StartDate.Equal(a.StartDate) || bt != at {
			c.Date = &DateChange{From: b.StartDate, To: a.StartDate, FromTBD: bt, ToTBD: at}
		}
		if b.Status != a.Status {
			c.Status = &StatusChange{From: b.Status, To: a.Status}
		}
		if c.Link || c.Date != nil || c.Status != nil {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(x, y Change) int { return cmp.Compare(x.EventID, y.EventID) })
	return out
}
