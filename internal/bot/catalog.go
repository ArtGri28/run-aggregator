package bot

import (
	"slices"
	"time"

	"aggregator/internal/event"
)

// Dictionary — справочники бота для Mini App (Phase 15): корзины дистанций,
// подписи дисциплин, названия стран и источников, пределы и периоды дат.
// Единственный экспортируемый вход к ним: API читает те же переменные, что
// экраны бота, и две копии не расходятся. Значения — снимок на момент вызова;
// поведение бота от аксессора не зависит.
type Dictionary struct {
	// DefaultCountry — страна пользователя, не выбравшего её.
	DefaultCountry string
	// FavLimit — предел будущих избранных стартов.
	FavLimit int
	// MaxPage — предел номера страницы выдачи.
	MaxPage int
	// MaxCityInput — предел длины текста запроса города (в рунах).
	MaxCityInput int
	// MaxSearchCandidates — сколько будущих событий читает текстовый поиск.
	MaxSearchCandidates int
	// ValidCityKey — ключ города в грамматике бота.
	ValidCityKey func(key string) bool
	// Buckets — корзины дистанций по видам спорта; нет ключа — у вида
	// спорта нет фильтра дистанции.
	Buckets map[event.Sport][]Bucket
	// TypeLabels — подписи дисциплин по коду.
	TypeLabels map[event.Type]string
	// Types — дисциплины меню по видам спорта в порядке показа (без «Другого»).
	Types map[event.Sport][]event.Type
	// CountryNames — названия стран по коду ISO alpha-2.
	CountryNames map[string]string
	// SourceNames — подписи источников по коду.
	SourceNames map[string]string
	// ProfileGenders, ProfileAges, ProfileCountries, ProfileSports —
	// допустимые значения анкеты и их подписи в порядке кнопок бота.
	ProfileGenders, ProfileAges, ProfileCountries, ProfileSports []Option
	// Months — периоды-месяцы: текущий и 11 следующих.
	Months func(today time.Time) []Option
	// ValidPeriod — период дат ("cm", "nm", "mYYYYMM") в грамматике бота.
	ValidPeriod func(period string) bool
	// DateRange — диапазон дат периода включительно; нулевой to — без
	// верхней границы.
	DateRange func(period string, today time.Time) (from, to time.Time)
}

// Bucket — корзина дистанций: полуинтервал (Lo, Hi] в метрах и подпись.
type Bucket struct {
	Label  string
	Lo, Hi int
}

// Option — код значения и его подпись.
type Option struct{ Code, Label string }

// Catalog возвращает справочники бота.
func Catalog() Dictionary {
	c := Dictionary{
		DefaultCountry: defaultCountry,
		FavLimit:       favLimit,
		MaxPage:        maxPage,
		MaxCityInput:   maxCityInput,
		ValidCityKey:   validCityKey,
		Buckets:        map[event.Sport][]Bucket{},
		TypeLabels:     typeLabels,
		Types:          map[event.Sport][]event.Type{},
		CountryNames:   countryNames,
		SourceNames:    sourceNames,
		ValidPeriod:    validPeriod,
		DateRange:      dateRange,

		ProfileGenders:   profileOptions(profileGenders),
		ProfileAges:      profileOptions(profileAges),
		ProfileCountries: profileOptions(profileCountries),
		ProfileSports:    profileOptions(profileSports),
		Months: func(today time.Time) []Option {
			var out []Option
			for _, o := range monthOptions(today) {
				out = append(out, Option{o.Value, o.Label})
			}
			return out
		},

		MaxSearchCandidates: maxSearchCandidates,
	}
	for _, sp := range event.Sports {
		for _, b := range sportBuckets(sp.Code) {
			c.Buckets[sp.Code] = append(c.Buckets[sp.Code], Bucket{b.label, b.lo, b.hi})
		}
		// Порядок как в меню дисциплин бота: у бега — menuTypes, у остальных —
		// перечень справочника без «Другого».
		if sp.Code == event.SportRunning {
			c.Types[sp.Code] = menuTypes[:]
		} else if types, ok := event.SportTypes[sp.Code]; ok {
			c.Types[sp.Code] = slices.DeleteFunc(slices.Clone(types), func(t event.Type) bool { return t == event.TypeOther })
		}
	}
	return c
}

// profileOptions — справочник анкеты в экспортируемом виде.
func profileOptions(in []profileOption) []Option {
	out := make([]Option, 0, len(in))
	for _, o := range in {
		out = append(out, Option(o))
	}
	return out
}
