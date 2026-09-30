package event

import "time"

// DistanceRange — диапазон дистанции (Lo, Hi] в метрах: Lo не входит, Hi входит.
type DistanceRange struct{ Lo, Hi int }

// Filter — параметры выдачи бота. From и To — календарные даты (полночь UTC),
// включительно; нулевой To — без верхней границы. Пустые CityKey, Type
// и Distances означают «любой»; событие подходит, если хотя бы одна его
// дистанция попадает хотя бы в один из Distances.
type Filter struct {
	From, To  time.Time
	CityKey   string
	Type      Type
	Distances []DistanceRange
}
