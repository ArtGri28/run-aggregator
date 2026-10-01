package bot

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"aggregator/internal/event"
)

// Кодек callback_data — контракт для всех экранов; предел Telegram — 64 байта.
//
//	f:s                      сводка
//	f:d[:a|30|cm|nm|mYYYYMM] дата: подменю / значение
//	f:c[:-|<city_key>]       город
//	f:b[:-|0..4]             корзина дистанции
//	f:t[:-|<тип>]            тип
//	f:x[:d|c|b|t]            сбросить всё / один фильтр
//	p:<стр>:v<N>             страница списка (N — версия фильтров)
//	e:<id>:<стр>:v<N>        карточка
//	r:<a|y|n>:<runID>        админ: принятие прогона
//	d:o                      админ: очередь дублей с начала
//	d:<m|y|n|x|s>:<cid>      админ: слить (подтверждение) / да / отмена / разные / пропустить
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
)

// callback — разобранная кнопка. Value: "" — открыть подменю, "-" — снять
// значение; для f:x — вид сброса ("" — все фильтры).
type callback struct {
	Kind  cbKind
	Value string
	Page  int
	ID    int64
	V     int
}

var (
	errCallback = errors.New("неверный callback")
	cityKeyRE   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	monthRE     = regexp.MustCompile(`^m(20[0-9]{2})(0[1-9]|1[0-2])$`)
)

const maxCityKeyBytes = 60 // 64 − len("f:c:")

var fKinds = map[string]cbKind{"s": cbSummary, "d": cbDate, "c": cbCity, "b": cbBucket, "t": cbType, "x": cbReset}
var fLetters = map[cbKind]string{cbSummary: "s", cbDate: "d", cbCity: "c", cbBucket: "b", cbType: "t", cbReset: "x"}

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

func validDate(s string) bool {
	switch s {
	case "a", "30", "cm", "nm":
		return true
	}
	return monthRE.MatchString(s)
}

func validCityKey(s string) bool { return len(s) <= maxCityKeyBytes && cityKeyRE.MatchString(s) }

// validValue проверяет значение f:<вид>:<value>.
func validValue(k cbKind, v string) bool {
	switch k {
	case cbDate:
		return validDate(v)
	case cbCity:
		return v == "-" || validCityKey(v)
	case cbBucket:
		return v == "-" || (len(v) == 1 && v[0] >= '0' && v[0] < '0'+byte(len(buckets)))
	case cbType:
		return v == "-" || validType(v)
	case cbReset:
		return v == "d" || v == "c" || v == "b" || v == "t"
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
	n, err := num(s[1:], 1, math.MaxInt32)
	return int(n), err
}

const maxPage = 10000

// parseCallback строго разбирает callback_data; всё вне грамматики — ошибка.
func parseCallback(data string) (callback, error) {
	p := strings.Split(data, ":")
	switch p[0] {
	case "f":
		if len(p) < 2 || len(p) > 3 {
			return callback{}, errCallback
		}
		k, ok := fKinds[p[1]]
		if !ok {
			return callback{}, errCallback
		}
		c := callback{Kind: k}
		if len(p) == 3 {
			if !validValue(k, p[2]) {
				return callback{}, errCallback
			}
			c.Value = p[2]
		}
		return c, nil
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
	}
	return callback{}, errCallback
}

// encode строит callback_data; ошибка — если результат длиннее 64 байт
// или не разбирается обратно (кнопку тогда пропускают, а не шлют в Telegram).
func (c callback) encode() (string, error) {
	var s string
	switch c.Kind {
	case cbSummary, cbDate, cbCity, cbBucket, cbType, cbReset:
		s = "f:" + fLetters[c.Kind]
		if c.Value != "" {
			s += ":" + c.Value
		}
	case cbPage:
		s = fmt.Sprintf("p:%d:v%d", c.Page, c.V)
	case cbCard:
		s = fmt.Sprintf("e:%d:%d:v%d", c.ID, c.Page, c.V)
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
