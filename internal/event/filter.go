package event

import "time"

// DistanceRange — диапазон дистанции (Lo, Hi] в метрах: Lo не входит, Hi входит.
type DistanceRange struct{ Lo, Hi int }

// DateRange — период дат включительно (полночь UTC).
type DateRange struct{ From, To time.Time }

// Filter — параметры выдачи бота. From и To — календарные даты (полночь UTC),
// включительно; нулевой To — без верхней границы. Пустые CountryCode,
// CityKey, Type и Distances означают «любой»; событие подходит, если хотя бы
// одна его дистанция попадает хотя бы в один из Distances. Город
// определяется парой (CountryCode, CityKey): одинаковый CityKey в разных
// странах — разные города, поэтому CityKey осмыслен вместе с CountryCode.
type Filter struct {
	From, To    time.Time
	CountryCode string // ISO alpha-2; "" — любая страна
	CityKey     string
	Type        Type
	Distances   []DistanceRange
	// Periods — дополнительно к From и To: дата события попадает хотя бы в
	// один период; пусто — без ограничения.
	Periods []DateRange
}
