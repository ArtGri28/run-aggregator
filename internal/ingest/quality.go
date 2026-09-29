package ingest

import (
	"fmt"
	"slices"
)

// RunStatus — итог прогона (ingest_runs.status).
type RunStatus string

const (
	// StatusOK — прогон применяется.
	StatusOK RunStatus = "ok"
	// StatusSuspect — прогон сохранён, но каталог не меняет до принятия.
	StatusSuspect RunStatus = "suspect"
	// StatusFailed выставляет вызывающий код: ошибка загрузки или
	// структуры, паника. Evaluate его не возвращает.
	StatusFailed RunStatus = "failed"
)

// Thresholds — пороги оценки прогона (MVP.md §5.4); значения по
// умолчанию — DefaultThresholds, переопределяются окружением.
type Thresholds struct {
	// MinRuns — сколько прогонов ok нужно, чтобы сравнивать с базой.
	MinRuns int
	// LowRatio, HighRatio — parsed ниже LowRatio или выше HighRatio
	// медианы базы → suspect.
	LowRatio, HighRatio float64
	// RejectedMax — доля rejected при наличии базы; RejectedAbs — без неё.
	RejectedMax, RejectedAbs float64
	// DateOORMax — доля дат вне диапазона.
	DateOORMax float64
	// OutOfScopeMax, UnrecognizedMax — доли от valid (D-09).
	OutOfScopeMax, UnrecognizedMax float64
	// EmptyGrowth — рост доли пустых city/region/distances относительно
	// базы, в долях единицы (0.20 = 20 п.п.).
	EmptyGrowth float64
}

// DefaultThresholds — стартовые значения MVP.md §5.4 и D-09.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinRuns: 3, LowRatio: 0.5, HighRatio: 5,
		RejectedMax: 0.20, RejectedAbs: 0.30, DateOORMax: 0.05,
		OutOfScopeMax: 0.20, UnrecognizedMax: 0.10, EmptyGrowth: 0.20,
	}
}

// Baseline — база сравнения: прогоны ok с последнего принятого вручную.
type Baseline struct {
	OKRuns int
	// Parsed — parsed этих прогонов (не больше 7 последних).
	Parsed []int
	// EmptyShare — доли пустых полей базы (ключи city, region, distances).
	EmptyShare map[string]float64
}

// emptyFields — ключевые поля, рост пустых значений которых проверяется.
var emptyFields = []string{"city", "region", "distances"}

// Evaluate оценивает прогон по MVP.md §5.4 с уточнениями D-05, D-09,
// D-11. Причины — строки по-русски с фактическими числами; нет причин —
// StatusOK. Знаменатель долей rejected и дат вне диапазона —
// parsed − expired (D-11); out_of_scope и нераспознанных областей — valid.
func Evaluate(s Stats, b Baseline, t Thresholds) (RunStatus, []string) {
	var why []string
	checked := s.Parsed - s.Expired
	over := func(n, of int, max float64, what string) {
		if r := share(n, of); r > max {
			why = append(why, fmt.Sprintf("%s: %.1f %% (%d из %d) выше порога %.1f %%", what, r*100, n, of, max*100))
		}
	}

	if b.OKRuns == 0 {
		why = append(why, "первый прогон источника — требуется принятие")
	}
	if b.OKRuns < t.MinRuns {
		over(s.Rejected, checked, t.RejectedAbs, "доля отбракованных")
	} else {
		m := median(b.Parsed)
		if p := float64(s.Parsed); p < t.LowRatio*m {
			why = append(why, fmt.Sprintf("parsed %d меньше %.0f %% медианы базы %.1f", s.Parsed, t.LowRatio*100, m))
		} else if p > t.HighRatio*m {
			why = append(why, fmt.Sprintf("parsed %d больше %.1f× медианы базы %.1f", s.Parsed, t.HighRatio, m))
		}
		over(s.Rejected, checked, t.RejectedMax, "доля отбракованных")
		for _, f := range emptyFields {
			cur, base := share(s.Empty[f], s.Valid), b.EmptyShare[f]
			if cur-base > t.EmptyGrowth {
				why = append(why, fmt.Sprintf("доля пустых %s выросла с %.1f %% до %.1f %% (порог роста %.1f п.п.)", f, base*100, cur*100, t.EmptyGrowth*100))
			}
		}
	}
	over(s.DateOutOfRange, checked, t.DateOORMax, "доля дат вне диапазона")
	over(s.OutOfScope, s.Valid, t.OutOfScopeMax, "доля вне каталога (out_of_scope)")
	over(s.UnrecognizedRegion, s.Valid, t.UnrecognizedMax, "доля нераспознанных областей")

	if len(why) > 0 {
		return StatusSuspect, why
	}
	return StatusOK, nil
}

// share — n/of; 0 при of <= 0.
func share(n, of int) float64 {
	if of <= 0 {
		return 0
	}
	return float64(n) / float64(of)
}

// median — медиана без изменения входа; чётное число — среднее двух
// средних; пустой вход — 0.
func median(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2
}
