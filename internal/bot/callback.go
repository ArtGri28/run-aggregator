package bot

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"aggregator/internal/event"
)

// Кодек callback_data — контракт для всех экранов; предел Telegram — 64 байта.
//
//	f:s:v<N>                      сводка (N — версия фильтров пользователя)
//	f:d[:a|30|cm|nm|mYYYYMM]:v<N> дата: подменю / одно значение (так задают кнопки старых сообщений)
//	f:w:<-|cm|nm|mYYYYMM>:v<N>    период дат: снять все / отметить или снять один
//	f:c[:-|<city_key>]:v<N>       город
//	f:n[:-|0..3]:v<N>             корзина дистанции выбранного вида спорта
//	f:b[:-]:v<N>                  то же в сообщениях до смены корзин: только открыть и снять
//	f:t[:-|<тип>]:v<N>            тип (дисциплина) выбранного вида спорта
//	f:a:<код>:v<N>                выбрать вид спорта: код справочника event.Sports
//	f:k[:-|*|<ISO>]:v<N>          страна: подменю / по умолчанию / все страны / код
//	f:x:<d|c|b|t|k>:v<N>          снять один фильтр
//	s:o:v<N>                      мои настройки
//	s:<r|y>:<o|f>:v<N>            сброс настроек: спросить / подтвердить; o — из настроек, f — из поиска
//	m                        главное меню (от фильтров не зависит — без версии)
//	p:<стр>:v<N>             страница списка
//	e:<id>:<стр>:v<N>        карточка
//	g:<id>:v<N>              карточка из результатов текстового поиска
//	g:b:v<N>                 назад к результатам поиска (запрос — в тексте сообщения)
//	r:<a|y|n>:<runID>        админ: принятие прогона
//	d:o                      админ: очередь дублей с начала
//	d:<m|y|n|x|s>:<cid>      админ: слить (подтверждение) / да / отмена / разные / пропустить
//	u:o                              мой профиль (без версии)
//	u:<b|n|z>:v<P>                   анкета: начать / пропустить / готово
//	u:a:<g|a|k|c|s>:<w|e>:v<P>       показать вопрос (w — анкета, e — правка ответа)
//	u:s:<поле>:<значение>:<w|e>:v<P> сохранить ответ
//	h:<o|r|x>                        поддержка: открыть из меню / ответить / отмена (без версии)
//
// Версия — всегда последнее поле. f: без версии — формат до Phase 8 (кнопки
// старых сообщений): разбирается с Legacy и считается устаревшим; encode его
// не создаёт. N = 0 — пользователя нет в users. У u: версия — profile_v
// (версия профиля, не фильтров); P = 0 — строки профиля нет.
const maxCallbackBytes = 64

type cbKind int

const (
	cbSummary cbKind = iota + 1
	cbDate
	cbCity
	cbBucket
	cbType
	cbReset
	cbPage
	cbCard
	cbRunAsk
	cbRunYes
	cbRunNo
	cbDupeOpen
	cbDupeAsk
	cbDupeYes
	cbDupeNo
	cbDupeRej
	cbDupeSkip
	cbCountry
	cbMenu
	cbSettings
	cbResetAsk
	cbResetYes
	cbPeriod
	cbSearchCard
	cbSearchBack
	cbProfile   // u:o
	cbProfStart // u:b
	cbProfSkip  // u:n
	cbProfAsk   // u:a
	cbProfSave  // u:s
	cbProfDone  // u:z

	cbSupport       // h:o
	cbSupportReply  // h:r
	cbSupportCancel // h:x

	cbSport // f:a
)

// callback — разобранная кнопка. Value: "" — открыть подменю, "-" — снять
// значение; для f:x — снимаемый фильтр; для сброса настроек — откуда пришли
// ("o" — настройки, "f" — поиск); для u:s — сохраняемое значение. Field и
// Mode — поле анкеты (g, a, k, c, s) и режим экрана (w, e) у кнопок u:a и u:s.
// Legacy — f: без версии.
type callback struct {
	Kind   cbKind
	Value  string
	Field  string
	Mode   string
	Page   int
	ID     int64
	V      int
	Legacy bool
}

var (
	errCallback = errors.New("неверный callback")
	cityKeyRE   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	countryRE   = regexp.MustCompile(`^[A-Z]{2}$`)
	monthRE     = regexp.MustCompile(`^m(20[0-9]{2})(0[1-9]|1[0-2])$`)
)

const maxCityKeyBytes = 60 // 64 − len("f:c:")

// maxProfileCityKeyBytes — предел ключа города в кнопке анкеты: 64 − len("u:s:c:")
// − len(":w:v") − 5 цифр версии. С версией от 100000 кнопка такого города не
// помещается и не показывается — город вводят текстом.
const maxProfileCityKeyBytes = 49

// profileOption — допустимое значение поля анкеты (D-04) и подпись кнопки.
type profileOption struct{ Code, Label string }

// Справочники D-04: они же единственный источник допустимых значений кнопок
// u:s; всё иное кодек отвергает. Порядок — порядок кнопок и видов спорта в
// профиле.
var (
	profileGenders   = []profileOption{{"m", "Мужской"}, {"f", "Женский"}}
	profileAges      = []profileOption{{"0", "До 18"}, {"1", "18–24"}, {"2", "25–34"}, {"3", "35–44"}, {"4", "45–54"}, {"5", "55+"}}
	profileCountries = []profileOption{{"RU", "🇷🇺 Россия"}, {"BY", "🇧🇾 Беларусь"}, {"KZ", "🇰🇿 Казахстан"}, {"ZZ", "🌍 Другая страна"}}
	profileSports    = []profileOption{
		{"run", "🏃 Бег"}, {"trail", "🏔 Трейлраннинг"}, {"ocr", "🧗 OCR · гонки с препятствиями"}, {"bike", "🚴 Велоспорт"},
		{"swim", "🏊 Плавание"}, {"ski", "⛷ Лыжные гонки"}, {"tri", "🏅 Триатлон"}, {"row", "🚣 Гребля"},
	}
)

func hasOption(opts []profileOption, code string) bool {
	return slices.ContainsFunc(opts, func(o profileOption) bool { return o.Code == code })
}

// validProfileValue проверяет значение u:s:<поле>:<значение>.
func validProfileValue(field, v string) bool {
	switch field {
	case "g":
		return hasOption(profileGenders, v)
	case "a":
		return hasOption(profileAges, v)
	case "k":
		return hasOption(profileCountries, v)
	case "c":
		return len(v) <= maxProfileCityKeyBytes && cityKeyRE.MatchString(v)
	case "s":
		return hasOption(profileSports, v)
	}
	return false
}

func validProfileField(f string) bool { return len(f) == 1 && strings.Contains("gakcs", f) }
func validProfileMode(m string) bool  { return m == modeWizard || m == modeEdit }

var profLetters = map[cbKind]string{cbProfStart: "b", cbProfSkip: "n", cbProfDone: "z"}

// Корзины дистанций сменились: новые кнопки — f:n. Номер корзины в f:b из
// старого сообщения значил бы другой диапазон, поэтому такая кнопка устарела.
var fKinds = map[string]cbKind{"s": cbSummary, "d": cbDate, "w": cbPeriod, "c": cbCity, "b": cbBucket, "n": cbBucket, "t": cbType, "k": cbCountry, "x": cbReset, "a": cbSport}
var fLetters = map[cbKind]string{cbSummary: "s", cbDate: "d", cbPeriod: "w", cbCity: "c", cbBucket: "n", cbType: "t", cbCountry: "k", cbReset: "x", cbSport: "a"}

// Кнопки поддержки не меняют ни профиль, ни фильтры, поэтому версии у них нет.
var hKinds = map[string]cbKind{"o": cbSupport, "r": cbSupportReply, "x": cbSupportCancel}
var hLetters = map[cbKind]string{cbSupport: "o", cbSupportReply: "r", cbSupportCancel: "x"}

var dKinds = map[string]cbKind{"m": cbDupeAsk, "y": cbDupeYes, "n": cbDupeNo, "x": cbDupeRej, "s": cbDupeSkip}
var dLetters = map[cbKind]string{cbDupeAsk: "m", cbDupeYes: "y", cbDupeNo: "n", cbDupeRej: "x", cbDupeSkip: "s"}

var eventTypes = []event.Type{event.TypeRoad, event.TypeTrail, event.TypeCross, event.TypeTrack, event.TypeObstacle, event.TypeOther}

func validType(s string) bool {
	for _, t := range eventTypes {
		if string(t) == s {
			return true
		}
	}
	return false
}

// validAnyType — дисциплина любого вида спорта справочника: кнопка f:t не
// знает выбранного вида спорта, подходит ли ему значение — решает onFilter.
func validAnyType(s string) bool {
	for _, types := range event.SportTypes {
		if slices.Contains(types, event.Type(s)) {
			return true
		}
	}
	return false
}

func validDate(s string) bool {
	switch s {
	case "a", "30", "cm", "nm":
		return true
	}
	return monthRE.MatchString(s)
}

// validPeriod — период дат, который можно отметить вместе с другими.
func validPeriod(s string) bool { return s == "cm" || s == "nm" || monthRE.MatchString(s) }

func validCityKey(s string) bool { return len(s) <= maxCityKeyBytes && cityKeyRE.MatchString(s) }

// validCountry — значение фильтра страны: "" (по умолчанию), "*" (все страны)
// или код ISO alpha-2 заглавными. Входит ли страна в каталог — не здесь.
func validCountry(s string) bool { return s == "" || s == "*" || countryRE.MatchString(s) }

// validValue проверяет значение f:<вид>:<value>.
func validValue(k cbKind, v string) bool {
	switch k {
	case cbDate:
		return validDate(v)
	case cbPeriod:
		return v == "-" || validPeriod(v)
	case cbCity:
		return v == "-" || validCityKey(v)
	case cbBucket:
		// Упрощение: номер корзины проверяется по числу беговых корзин — наборы
		// всех видов спорта одного размера (TestSkiBuckets). Набор другого
		// размера потребует проверки по выбранному виду спорта в onFilter.
		return v == "-" || (len(v) == 1 && v[0] >= '0' && v[0] < '0'+byte(len(buckets)))
	case cbType:
		return v == "-" || validAnyType(v)
	case cbCountry:
		return v == "-" || (v != "" && validCountry(v))
	case cbSport:
		_, ok := event.SportByCode(event.Sport(v))
		return ok
	case cbReset:
		return v == "d" || v == "c" || v == "b" || v == "t" || v == "k"
	}
	return false
}

// num разбирает десятичное число без знака и ведущих нулей в [min, max].
func num(s string, min, max int64) (int64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, errCallback
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errCallback
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < min || n > max {
		return 0, errCallback
	}
	return n, nil
}

func version(s string) (int, error) {
	if !strings.HasPrefix(s, "v") {
		return 0, errCallback
	}
	n, err := num(s[1:], 0, math.MaxInt32)
	return int(n), err
}

const maxPage = 10000

// parseCallback строго разбирает callback_data; всё вне грамматики — ошибка.
func parseCallback(data string) (callback, error) {
	if data == "m" {
		return callback{Kind: cbMenu}, nil
	}
	p := strings.Split(data, ":")
	switch p[0] {
	case "f":
		c := callback{Legacy: true}
		if v, err := version(p[len(p)-1]); err == nil && len(p) > 2 {
			c.V, c.Legacy, p = v, false, p[:len(p)-1]
		}
		if len(p) < 2 || len(p) > 3 {
			return callback{}, errCallback
		}
		k, ok := fKinds[p[1]]
		if !ok {
			return callback{}, errCallback
		}
		c.Kind = k
		if (k == cbReset || k == cbPeriod || k == cbSport) && len(p) != 3 {
			return callback{}, errCallback // «сбросить всё» — только через подтверждение (s:y); у периода и вида спорта значение обязательно
		}
		if len(p) == 3 {
			if !validValue(k, p[2]) || (p[1] == "b" && p[2] != "-") {
				return callback{}, errCallback
			}
			c.Value = p[2]
		}
		return c, nil
	case "s":
		if len(p) < 3 {
			return callback{}, errCallback
		}
		v, err := version(p[len(p)-1])
		if err != nil {
			return callback{}, err
		}
		switch {
		case len(p) == 3 && p[1] == "o":
			return callback{Kind: cbSettings, V: v}, nil
		case len(p) == 4 && (p[1] == "r" || p[1] == "y") && (p[2] == "o" || p[2] == "f"):
			k := cbResetAsk
			if p[1] == "y" {
				k = cbResetYes
			}
			return callback{Kind: k, Value: p[2], V: v}, nil
		}
		return callback{}, errCallback
	case "p":
		if len(p) != 3 {
			return callback{}, errCallback
		}
		page, err := num(p[1], 1, maxPage)
		if err != nil {
			return callback{}, err
		}
		v, err := version(p[2])
		return callback{Kind: cbPage, Page: int(page), V: v}, err
	case "e":
		if len(p) != 4 {
			return callback{}, errCallback
		}
		id, err := num(p[1], 1, math.MaxInt64)
		if err != nil {
			return callback{}, err
		}
		page, err := num(p[2], 1, maxPage)
		if err != nil {
			return callback{}, err
		}
		v, err := version(p[3])
		return callback{Kind: cbCard, ID: id, Page: int(page), V: v}, err
	case "g":
		if len(p) != 3 {
			return callback{}, errCallback
		}
		v, err := version(p[2])
		if err != nil {
			return callback{}, err
		}
		if p[1] == "b" {
			return callback{Kind: cbSearchBack, V: v}, nil
		}
		id, err := num(p[1], 1, math.MaxInt64)
		return callback{Kind: cbSearchCard, ID: id, V: v}, err
	case "r":
		if len(p) != 3 {
			return callback{}, errCallback
		}
		kinds := map[string]cbKind{"a": cbRunAsk, "y": cbRunYes, "n": cbRunNo}
		k, ok := kinds[p[1]]
		if !ok {
			return callback{}, errCallback
		}
		id, err := num(p[2], 1, math.MaxInt64)
		return callback{Kind: k, ID: id}, err
	case "d":
		if len(p) == 2 && p[1] == "o" {
			return callback{Kind: cbDupeOpen}, nil
		}
		if len(p) != 3 {
			return callback{}, errCallback
		}
		k, ok := dKinds[p[1]]
		if !ok {
			return callback{}, errCallback
		}
		id, err := num(p[2], 1, math.MaxInt64)
		return callback{Kind: k, ID: id}, err
	case "u":
		return parseProfile(p)
	case "h":
		if len(p) == 2 {
			if k, ok := hKinds[p[1]]; ok {
				return callback{Kind: k}, nil
			}
		}
		return callback{}, errCallback
	}
	return callback{}, errCallback
}

// parseProfile разбирает u:* по грамматике D-14: версия — последнее поле,
// значения — только из справочников профиля, всё иное — errCallback.
func parseProfile(p []string) (callback, error) {
	if len(p) == 2 && p[1] == "o" {
		return callback{Kind: cbProfile}, nil
	}
	if len(p) < 3 {
		return callback{}, errCallback
	}
	v, err := version(p[len(p)-1])
	if err != nil {
		return callback{}, err
	}
	switch {
	case len(p) == 3 && p[1] == "b":
		return callback{Kind: cbProfStart, V: v}, nil
	case len(p) == 3 && p[1] == "n":
		return callback{Kind: cbProfSkip, V: v}, nil
	case len(p) == 3 && p[1] == "z":
		return callback{Kind: cbProfDone, V: v}, nil
	case len(p) == 5 && p[1] == "a" && validProfileField(p[2]) && validProfileMode(p[3]):
		return callback{Kind: cbProfAsk, Field: p[2], Mode: p[3], V: v}, nil
	case len(p) == 6 && p[1] == "s" && validProfileField(p[2]) && validProfileValue(p[2], p[3]) && validProfileMode(p[4]):
		return callback{Kind: cbProfSave, Field: p[2], Value: p[3], Mode: p[4], V: v}, nil
	}
	return callback{}, errCallback
}

// encode строит callback_data; ошибка — если результат длиннее 64 байт
// или не разбирается обратно (кнопку тогда пропускают, а не шлют в Telegram).
func (c callback) encode() (string, error) {
	var s string
	switch c.Kind {
	case cbSummary, cbDate, cbPeriod, cbCity, cbBucket, cbType, cbCountry, cbReset, cbSport:
		s = "f:" + fLetters[c.Kind]
		if c.Value != "" {
			s += ":" + c.Value
		}
		s += ":v" + strconv.Itoa(c.V)
	case cbMenu:
		s = "m"
	case cbSettings:
		s = fmt.Sprintf("s:o:v%d", c.V)
	case cbResetAsk:
		s = fmt.Sprintf("s:r:%s:v%d", c.Value, c.V)
	case cbResetYes:
		s = fmt.Sprintf("s:y:%s:v%d", c.Value, c.V)
	case cbPage:
		s = fmt.Sprintf("p:%d:v%d", c.Page, c.V)
	case cbCard:
		s = fmt.Sprintf("e:%d:%d:v%d", c.ID, c.Page, c.V)
	case cbSearchCard:
		s = fmt.Sprintf("g:%d:v%d", c.ID, c.V)
	case cbSearchBack:
		s = fmt.Sprintf("g:b:v%d", c.V)
	case cbRunAsk:
		s = fmt.Sprintf("r:a:%d", c.ID)
	case cbRunYes:
		s = fmt.Sprintf("r:y:%d", c.ID)
	case cbRunNo:
		s = fmt.Sprintf("r:n:%d", c.ID)
	case cbDupeOpen:
		s = "d:o"
	case cbDupeAsk, cbDupeYes, cbDupeNo, cbDupeRej, cbDupeSkip:
		s = fmt.Sprintf("d:%s:%d", dLetters[c.Kind], c.ID)
	case cbProfile:
		s = "u:o"
	case cbProfStart, cbProfSkip, cbProfDone:
		s = fmt.Sprintf("u:%s:v%d", profLetters[c.Kind], c.V)
	case cbProfAsk:
		s = fmt.Sprintf("u:a:%s:%s:v%d", c.Field, c.Mode, c.V)
	case cbProfSave:
		s = fmt.Sprintf("u:s:%s:%s:%s:v%d", c.Field, c.Value, c.Mode, c.V)
	case cbSupport, cbSupportReply, cbSupportCancel:
		s = "h:" + hLetters[c.Kind]
	default:
		return "", errCallback
	}
	if len(s) > maxCallbackBytes {
		return "", fmt.Errorf("callback длиннее %d байт", maxCallbackBytes)
	}
	if _, err := parseCallback(s); err != nil {
		return "", err
	}
	return s, nil
}
