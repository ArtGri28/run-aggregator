// Package event holds the domain types shared by store and every future
// consumer (ingest, match, bot — Phase 3+). MVP.md §3.2, §4.2, §4.4.
package event

import (
	"slices"
	"time"
)

// Status is the fate of the competition itself (MVP.md §4.4) — changed
// only by an explicit status from the source, never by observation state.
type Status string

const (
	StatusScheduled Status = "scheduled"
	StatusCancelled Status = "cancelled"
	StatusPostponed Status = "postponed"
)

// Type is one of the six race types (MVP.md §2.2). Ultra is not a type —
// it's a property of the max distance, derived by query.
type Type string

const (
	TypeRoad     Type = "road"
	TypeTrail    Type = "trail"
	TypeCross    Type = "cross"
	TypeTrack    Type = "track"
	TypeObstacle Type = "obstacle"
	TypeOther    Type = "other"
)

// Sport — код вида спорта события (колонка events.sport).
type Sport string

const (
	SportRunning    Sport = "running"
	SportSkiing     Sport = "skiing"
	SportSwimming   Sport = "swimming"
	SportCycling    Sport = "cycling"
	SportMultisport Sport = "multisport"
)

// SportInfo — запись справочника видов спорта.
type SportInfo struct {
	Code  Sport
	Title string
	Emoji string
	// Enabled — показывает ли бот события этого вида спорта.
	Enabled bool
}

// Sports — справочник видов спорта, один на проект (D-01). Enabled решает,
// показывает ли бот события вида спорта: пока включён только бег (D-02).
// OCR и трейл — типы бегового события (Type), а не виды спорта (D-03).
var Sports = [...]SportInfo{
	{SportRunning, "Бег", "🏃", true},
	{SportSkiing, "Лыжи", "⛷", false},
	{SportSwimming, "Плавание", "🏊", false},
	{SportCycling, "Велоспорт", "🚴", false},
	{SportMultisport, "Мультиспорт", "🏅", false},
}

// Дисциплины лыж: значения events.event_type у событий вида спорта skiing.
const (
	TypeSki       Type = "ski"
	TypeRollerski Type = "rollerski"
)

// SportTypes — допустимые дисциплины по видам спорта. Схема проверяет только
// формат event_type (миграция 00009); перечень значений — здесь. TypeOther
// допустим у любого вида спорта.
var SportTypes = map[Sport][]Type{
	SportRunning: {TypeRoad, TypeTrail, TypeCross, TypeTrack, TypeObstacle, TypeOther},
	SportSkiing:  {TypeSki, TypeRollerski, TypeOther},
}

// TypeFor — дисциплина t, если она допустима у вида спорта s, иначе TypeOther.
func TypeFor(s Sport, t Type) Type {
	if slices.Contains(SportTypes[s], t) {
		return t
	}
	return TypeOther
}

// SportOf — вид спорта по коду записи: пустой код означает бег (D-05),
// любой другой возвращается как есть.
func SportOf(code string) Sport {
	if code == "" {
		return SportRunning
	}
	return Sport(code)
}

// SportByCode — запись справочника по коду; false — кода в справочнике нет.
func SportByCode(code Sport) (SportInfo, bool) {
	for _, s := range Sports {
		if s.Code == code {
			return s, true
		}
	}
	return SportInfo{}, false
}

// MatchState is source_events.match_state — how a source's raw record
// relates to a canonical event (MVP.md §4.1).
type MatchState string

const (
	MatchNew        MatchState = "new"
	MatchAuto       MatchState = "auto"
	MatchManual     MatchState = "manual"
	MatchCandidate  MatchState = "candidate"
	MatchOutOfScope MatchState = "out_of_scope"
)

// Distance is one race distance in meters with an optional display label
// (MVP.md §4.3). Label "" is the NULL-equivalent.
type Distance struct {
	Meters int
	Label  string
}

// Event is the canonical race (MVP.md §4.1, §4.2). Optional text fields
// use "" for NULL; EndDate and MergedIntoID use nil for NULL.
type Event struct {
	ID    int64
	Title string
	// TitleNorm is a deterministic function of Title, used for dedup.
	TitleNorm string
	// StartDate is the local wall-clock date (no timezone conversion).
	StartDate time.Time
	EndDate   *time.Time
	// StartTime is "HH:MM" local time, "" if the source gave none.
	StartTime string
	// Timezone is an IANA name (e.g. "Europe/Moscow"), "" if unknown.
	Timezone    string
	CountryCode string
	Region      string
	City        string
	CityKey     string
	Address     string
	Type        Type
	// Sport — код из справочника видов спорта; пустое значение на входе
	// означает бег (D-05).
	Sport           Sport
	Status          Status
	Description     string
	RegistrationURL string
	Distances       []Distance
	// IsListed and MergedIntoID govern visibility and merge (MVP.md §4.5);
	// SaveEvent does not change them.
	IsListed     bool
	MergedIntoID *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
