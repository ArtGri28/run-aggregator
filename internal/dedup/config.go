// Package dedup ищет дубли событий между источниками: схожесть названий,
// решение по паре, настройки DEDUP_* и проход Pass по базе после прогона.
package dedup

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"aggregator/internal/event"
)

// Config — настройки дедупликации (DDP-02); по умолчанию — DefaultConfig,
// переопределяются окружением в LoadConfig.
type Config struct {
	// High, Low — пороги схожести: не ниже High — кандидат на автослияние,
	// ниже Low — отдельные события.
	High, Low float64
	// AutoMerge включает автослияние; выключено до калибровки (D-08).
	AutoMerge bool
	// LinkBonus прибавляется к схожести при общей ссылке регистрации,
	// только если схожесть названий не ниже Low (DDP-04).
	LinkBonus float64
	// MaxPerEvent — не больше стольких лучших кандидатов на событие.
	MaxPerEvent int
	// DistanceTolM — допуск при объединении дистанций слитого события, м.
	DistanceTolM int
	// SourcePriority — коды источников от главного: чьи поля берёт слитое
	// событие (D-07).
	SourcePriority []string
	// DigestAt — время ежедневной сводки кандидатов, "HH:MM" по Москве.
	DigestAt string
}

// maxDistanceTolM — больший допуск склеит соседние дистанции (5 и 10 км
// с погрешностью разметки) в слитом событии.
const maxDistanceTolM = 1000

// DefaultConfig — значения до калибровки (05-07); автослияние выключено.
func DefaultConfig() Config {
	return Config{
		High:           0.7,
		Low:            0.3, // калибровка 05-07: при 0,4 пропущены c33–c35 (NameSim 0,34–0,38)
		AutoMerge:      false,
		LinkBonus:      0.10,
		MaxPerEvent:    3,
		DistanceTolM:   100,
		SourcePriority: []string{"marathonec", "probeg"},
		DigestAt:       "10:00",
	}
}

// Fold — настройки свёртки полей слитого события.
func (c Config) Fold() event.FoldConfig {
	return event.FoldConfig{Priority: c.SourcePriority, DistanceTolM: c.DistanceTolM}
}

// LoadConfig читает DEDUP_* поверх DefaultConfig; пустое значение = не
// задано. Автослияние включается только DEDUP_AUTO_MERGE=true после
// калибровки (D-08, 05-07). Читают worker и bot. Ошибка называет
// переменную, но не повторяет её значение.
func LoadConfig() (Config, error) {
	c := DefaultConfig()
	vars := []struct {
		name string
		dst  any
	}{
		{"DEDUP_HIGH", &c.High},
		{"DEDUP_LOW", &c.Low},
		{"DEDUP_AUTO_MERGE", &c.AutoMerge},
		{"DEDUP_LINK_BONUS", &c.LinkBonus},
		{"DEDUP_MAX_PER_EVENT", &c.MaxPerEvent},
		{"DEDUP_DISTANCE_TOL_M", &c.DistanceTolM},
		{"DEDUP_SOURCE_PRIORITY", &c.SourcePriority},
		{"DEDUP_DIGEST_AT", &c.DigestAt},
	}
	for _, v := range vars {
		s, ok := os.LookupEnv(v.name)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		if err := setValue(strings.TrimSpace(s), v.dst); err != nil {
			return Config{}, fmt.Errorf("%s: %w", v.name, err)
		}
	}

	_, digestErr := time.Parse("15:04", c.DigestAt)
	checks := []struct {
		name, msg string
		ok        bool
	}{
		{"DEDUP_HIGH", "должно быть больше нуля и не больше единицы", c.High > 0 && c.High <= 1},
		{"DEDUP_LOW", "должно быть больше нуля и не больше DEDUP_HIGH", c.Low > 0 && c.Low <= c.High},
		{"DEDUP_LINK_BONUS", "должно быть от нуля до единицы", c.LinkBonus >= 0 && c.LinkBonus <= 1},
		{"DEDUP_MAX_PER_EVENT", "должно быть больше нуля", c.MaxPerEvent > 0},
		{"DEDUP_DISTANCE_TOL_M", "должно быть от нуля до 1000 м", c.DistanceTolM >= 0 && c.DistanceTolM <= maxDistanceTolM},
		{"DEDUP_SOURCE_PRIORITY", "нужен непустой список кодов без повторов", len(c.SourcePriority) > 0 && unique(c.SourcePriority)},
		{"DEDUP_DIGEST_AT", "нужно время в формате ЧЧ:ММ", digestErr == nil},
	}
	for _, ch := range checks {
		if !ch.ok {
			return Config{}, fmt.Errorf("%s: %s", ch.name, ch.msg)
		}
	}
	return c, nil
}

// setValue разбирает s в *dst; текст ошибки не содержит s.
func setValue(s string, dst any) error {
	bad := errors.New("неверное значение")
	switch p := dst.(type) {
	case *string:
		*p = s
	case *float64:
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return bad
		}
		*p = x
	case *int:
		n, err := strconv.Atoi(s)
		if err != nil {
			return bad
		}
		*p = n
	case *bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return bad
		}
		*p = b
	case *[]string:
		var codes []string
		for _, code := range strings.Split(s, ",") {
			if code = strings.TrimSpace(code); code != "" {
				codes = append(codes, code)
			}
		}
		*p = codes
	default:
		panic(fmt.Sprintf("setValue: тип %T", dst))
	}
	return nil
}

func unique(ss []string) bool {
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		if seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
