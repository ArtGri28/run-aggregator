package event

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func ptr(t time.Time) *time.Time { return &t }

var prio = FoldConfig{Priority: []string{"marathonec", "probeg"}, DistanceTolM: 100}

func TestFold(t *testing.T) {
	m := Event{Title: "А", TitleNorm: "а", Type: TypeOther, RegistrationURL: "r1", StartDate: day(3), Region: "Москва"}
	p := Event{Title: "Б", TitleNorm: "б", Type: TypeTrail, Description: "д", StartDate: day(3), EndDate: ptr(day(4)),
		StartTime: "09:00", Region: "Москва", City: "Зеленоград", CityKey: "zelenograd", Timezone: "Europe/Moscow",
		Status: StatusCancelled, ID: 7, IsListed: true, MergedIntoID: new(int64)}

	f := Fold([]Part{{"probeg", p}, {"marathonec", m}}, prio)
	want := Event{Title: "А", TitleNorm: "а", Type: TypeTrail, RegistrationURL: "r1", Description: "д",
		StartDate: day(3), Region: "Москва", City: "Зеленоград", CityKey: "zelenograd", Timezone: "Europe/Moscow",
		Distances: []Distance{}}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("Fold =\n%+v\nwant\n%+v", f, want)
	}

	title := func(parts []Part, cfg FoldConfig) string { return Fold(parts, cfg).Title }
	z := Event{Title: "Z"}
	y := Event{Title: "Y"}
	for _, parts := range [][]Part{{{"zzz", z}, {"probeg", p}}, {{"probeg", p}, {"zzz", z}}} {
		if got := title(parts, prio); got != "Б" {
			t.Errorf("неизвестный код раньше известного: Title = %q", got)
		}
	}
	if got := title([]Part{{"zzz", z}, {"yyy", y}}, prio); got != "Z" {
		t.Errorf("два неизвестных: Title = %q, want порядок входа", got)
	}
	if got := title([]Part{{"probeg", p}, {"marathonec", m}}, FoldConfig{}); got != "Б" {
		t.Errorf("пустой Priority: Title = %q, want порядок входа", got)
	}
	if got := Fold([]Part{{"probeg", Event{Type: TypeOther}}, {"marathonec", Event{Type: TypeOther}}}, prio).Type; got != TypeOther {
		t.Errorf("все other: Type = %q", got)
	}
	if got := Fold(nil, prio); !reflect.DeepEqual(got, Event{}) {
		t.Errorf("пустой вход: %+v", got)
	}
}

func TestFoldDistances(t *testing.T) {
	m := Event{Distances: []Distance{{21097, "21,1 км"}, {42195, ""}}}
	p := Event{Distances: []Distance{{21100, "полумарафон"}, {10000, ""}, {42195, ""}}}
	meters := func(tol int) []Distance {
		return Fold([]Part{{"probeg", p}, {"marathonec", m}}, FoldConfig{Priority: prio.Priority, DistanceTolM: tol}).Distances
	}
	tests := []struct {
		name string
		got  []Distance
		want []Distance
	}{
		{"допуск 100", meters(100), []Distance{{10000, ""}, {21097, "21,1 км"}, {42195, ""}}},
		{"допуск 0", meters(0), []Distance{{10000, ""}, {21097, "21,1 км"}, {21100, "полумарафон"}, {42195, ""}}},
		{"отрицательный допуск = 0", meters(-5), []Distance{{10000, ""}, {21097, "21,1 км"}, {21100, "полумарафон"}, {42195, ""}}},
		{"внутри части — только точные повторы",
			Fold([]Part{{"probeg", Event{Distances: []Distance{{5000, "a"}, {5050, ""}, {5000, "b"}}}}}, prio).Distances,
			[]Distance{{5000, "a"}, {5050, ""}}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, tt.got, tt.want)
		}
		if !sort.SliceIsSorted(tt.got, func(i, j int) bool { return tt.got[i].Meters < tt.got[j].Meters }) {
			t.Errorf("%s: не отсортировано: %v", tt.name, tt.got)
		}
	}
}

// sameCanonical — поля canonicalFieldsEqual (store), кроме Status.
func sameCanonical(a, b Event) bool {
	endEq := a.EndDate == nil && b.EndDate == nil || a.EndDate != nil && b.EndDate != nil && a.EndDate.Equal(*b.EndDate)
	return a.Title == b.Title && a.TitleNorm == b.TitleNorm && a.StartDate.Equal(b.StartDate) && endEq &&
		a.StartTime == b.StartTime && a.Timezone == b.Timezone && a.CountryCode == b.CountryCode &&
		a.Region == b.Region && a.City == b.City && a.CityKey == b.CityKey && a.Address == b.Address &&
		a.Type == b.Type && a.Description == b.Description && a.RegistrationURL == b.RegistrationURL
}

// normalized — как store.normalizeDistances: точные повторы, метка первого, по метрам.
func normalized(in []Distance) []Distance {
	seen := map[int]bool{}
	out := []Distance{}
	for _, d := range in {
		if !seen[d.Meters] {
			seen[d.Meters] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meters < out[j].Meters })
	return out
}

func samples() []Event {
	return []Event{
		{Title: "Трейл", TitleNorm: "трейл", StartDate: day(3), CountryCode: "RU", Region: "Свердловская область",
			City: "Черноисточинск", CityKey: "chernoistochinsk", Type: TypeTrail, Status: StatusScheduled,
			RegistrationURL: "https://myrace.info/events/2080", Distances: []Distance{{20000, ""}, {2000, ""}, {10000, "10 км"}}},
		{Title: "Ночной", TitleNorm: "ночной", StartDate: day(24), EndDate: ptr(day(25)), StartTime: "21:00",
			Timezone: "Europe/Moscow", CountryCode: "RU", Region: "Рязанская область", City: "Рязань", CityKey: "ryazan",
			Address: "парк", Type: TypeOther, Status: StatusCancelled, Description: "д",
			Distances: []Distance{{5000, "a"}, {5000, "b"}, {1000, ""}}},
		{Title: "Без города", TitleNorm: "без города", StartDate: day(10), CountryCode: "RU", Region: "Москва", Type: TypeRoad},
	}
}

func TestFoldSingle(t *testing.T) {
	for _, e := range samples() {
		f := Fold([]Part{{"probeg", e}}, prio)
		if !sameCanonical(f, e) || !reflect.DeepEqual(f.Distances, normalized(e.Distances)) {
			t.Errorf("Fold одной части:\n%+v\nwant\n%+v", f, e)
		}
	}
}

func TestFoldDeterministic(t *testing.T) {
	cfg := FoldConfig{Priority: []string{"marathonec", "probeg", "russiarunning"}, DistanceTolM: 100}
	ev := samples()
	parts := []Part{{"probeg", ev[0]}, {"marathonec", ev[1]}, {"russiarunning", ev[2]}}
	orig := []Part{{"probeg", samples()[0]}, {"marathonec", samples()[1]}, {"russiarunning", samples()[2]}}

	want := Fold(parts, cfg)
	for _, perm := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		in := []Part{parts[perm[0]], parts[perm[1]], parts[perm[2]]}
		if got := Fold(in, cfg); !reflect.DeepEqual(got, want) {
			t.Fatalf("перестановка %v:\n%+v\nwant\n%+v", perm, got, want)
		}
	}
	if !reflect.DeepEqual(parts, orig) {
		t.Fatalf("Fold изменил вход:\n%+v", parts)
	}

	// Результат не делит память со входом.
	got := Fold(parts, cfg)
	got.Distances[0].Meters = -1
	*got.EndDate = day(1)
	if !reflect.DeepEqual(parts, orig) {
		t.Fatalf("изменение результата задело вход:\n%+v", parts)
	}
}
