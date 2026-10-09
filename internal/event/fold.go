package event

import "sort"

// FoldConfig — как собирать слитое событие из записей нескольких
// источников (D-07, DDP-10).
type FoldConfig struct {
	// Priority — коды источников от главного к второстепенному; неизвестный
	// код идёт после всех известных.
	Priority []string
	// DistanceTolM — допуск в метрах: дистанция другой записи в пределах
	// допуска от уже взятой считается той же. Отрицательный = 0.
	DistanceTolM int
}

// Part — поля одной записи источника для свёртки.
type Part struct {
	Source string
	Event  Event
}

// Fold собирает канонические поля слитого события: части упорядочиваются
// по Priority (при равном ранге — порядок входа), поле берётся из первой
// части, где оно непустое. Группы берутся целиком из одной части, чтобы не
// смешивать записи: заголовок (Title, TitleNorm), даты (StartDate, EndDate,
// StartTime — время одного источника не цепляется к дате другого), место
// (Region, City, CityKey, Timezone, Address — адрес не цепляется к чужому
// городу). Type — первый не other. Sport — из первой части, где он задан:
// виды спорта разных записей не смешиваются (D-07). Дистанции —
// объединение: значение другой части в пределах DistanceTolM от уже взятого
// отбрасывается, метка от первого, сортировка по метрам; одна часть даёт
// ровно normalizeDistances.
//
// Результат детерминирован и не зависит от порядка частей с разными
// известными кодами — иначе каждый прогон находил бы «изменения» (Pitfall 6).
// Вход не меняется. Status, ID, IsListed, MergedIntoID, CreatedAt, UpdatedAt
// не заполняются — их ставит вызывающий; пишется результат только через
// saveEventTx (D-17.1).
func Fold(parts []Part, cfg FoldConfig) Event {
	if len(parts) == 0 {
		return Event{}
	}
	ps := append([]Part(nil), parts...)
	rank := func(code string) int {
		for i, p := range cfg.Priority {
			if p == code {
				return i
			}
		}
		return len(cfg.Priority)
	}
	sort.SliceStable(ps, func(i, j int) bool { return rank(ps[i].Source) < rank(ps[j].Source) })

	// pick — первая часть, где выполнен первый подходящий предикат.
	pick := func(preds ...func(Event) bool) (Event, bool) {
		for _, ok := range preds {
			for _, p := range ps {
				if ok(p.Event) {
					return p.Event, true
				}
			}
		}
		return Event{}, false
	}
	first := func(get func(Event) string) string {
		for _, p := range ps {
			if s := get(p.Event); s != "" {
				return s
			}
		}
		return ""
	}

	var out Event
	if e, ok := pick(func(e Event) bool { return e.Title != "" }); ok {
		out.Title, out.TitleNorm = e.Title, e.TitleNorm
	}
	if e, ok := pick(func(e Event) bool { return !e.StartDate.IsZero() }); ok {
		out.StartDate, out.StartTime = e.StartDate, e.StartTime
		if e.EndDate != nil {
			end := *e.EndDate
			out.EndDate = &end
		}
	}
	if e, ok := pick(
		func(e Event) bool { return e.City != "" },
		func(e Event) bool { return e.Region != "" },
		func(e Event) bool { return e.Timezone != "" || e.Address != "" },
	); ok {
		out.Region, out.City, out.CityKey, out.Timezone, out.Address = e.Region, e.City, e.CityKey, e.Timezone, e.Address
	}
	out.CountryCode = first(func(e Event) string { return e.CountryCode })
	out.Description = first(func(e Event) string { return e.Description })
	out.RegistrationURL = first(func(e Event) string { return e.RegistrationURL })
	out.Type = Type(first(func(e Event) string {
		if e.Type == TypeOther {
			return ""
		}
		return string(e.Type)
	}))
	if out.Type == "" {
		out.Type = Type(first(func(e Event) string { return string(e.Type) }))
	}
	out.Sport = Sport(first(func(e Event) string { return string(e.Sport) }))

	tol := max(cfg.DistanceTolM, 0)
	out.Distances = make([]Distance, 0)
	for _, p := range ps {
		prev := len(out.Distances) // взятые из предыдущих частей
		for _, d := range p.Event.Distances {
			if hasMeters(out.Distances[:prev], d.Meters, tol) || hasMeters(out.Distances[prev:], d.Meters, 0) {
				continue
			}
			out.Distances = append(out.Distances, d)
		}
	}
	sort.Slice(out.Distances, func(i, j int) bool { return out.Distances[i].Meters < out.Distances[j].Meters })
	return out
}

func hasMeters(ds []Distance, m, tol int) bool {
	for _, d := range ds {
		if d.Meters-m <= tol && m-d.Meters <= tol {
			return true
		}
	}
	return false
}
