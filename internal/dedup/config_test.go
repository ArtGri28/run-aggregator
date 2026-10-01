package dedup

import (
	"reflect"
	"strings"
	"testing"

	"aggregator/internal/event"
)

// configVars — все переменные LoadConfig: тест очищает их, чтобы не
// зависеть от окружения того, кто запускает тесты (пустое = не задано).
var configVars = strings.Fields(`DEDUP_HIGH DEDUP_LOW DEDUP_AUTO_MERGE DEDUP_LINK_BONUS DEDUP_MAX_PER_EVENT
	DEDUP_DISTANCE_TOL_M DEDUP_SOURCE_PRIORITY DEDUP_DIGEST_AT`)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	want := Config{High: 0.7, Low: 0.3, AutoMerge: false, LinkBonus: 0.10, MaxPerEvent: 3, DistanceTolM: 100,
		SourcePriority: []string{"marathonec", "probeg"}, DigestAt: "10:00"}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("DefaultConfig = %+v", c)
	}
	c.SourcePriority[0] = "zzz"
	if DefaultConfig().SourcePriority[0] != "marathonec" {
		t.Fatal("DefaultConfig делит SourcePriority между вызовами")
	}
	if f := DefaultConfig().Fold(); !reflect.DeepEqual(f, event.FoldConfig{Priority: []string{"marathonec", "probeg"}, DistanceTolM: 100}) {
		t.Fatalf("Fold = %+v", f)
	}
}

func TestLoadConfig(t *testing.T) {
	for _, k := range configVars {
		t.Setenv(k, "")
	}
	t.Run("defaults", func(t *testing.T) {
		c, err := LoadConfig()
		if err != nil || !reflect.DeepEqual(c, DefaultConfig()) {
			t.Fatalf("LoadConfig = %+v, %v", c, err)
		}
	})
	t.Run("whitespace = unset", func(t *testing.T) {
		t.Setenv("DEDUP_HIGH", "  ")
		if c, err := LoadConfig(); err != nil || c.High != 0.7 {
			t.Fatalf("LoadConfig = %+v, %v", c, err)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		for k, v := range map[string]string{
			"DEDUP_HIGH": "0.8", "DEDUP_LOW": "0.5", "DEDUP_AUTO_MERGE": "true", "DEDUP_LINK_BONUS": "0.05",
			"DEDUP_MAX_PER_EVENT": "5", "DEDUP_DISTANCE_TOL_M": "50",
			"DEDUP_SOURCE_PRIORITY": " probeg , marathonec ", "DEDUP_DIGEST_AT": "09:30",
		} {
			t.Setenv(k, v)
		}
		c, err := LoadConfig()
		want := Config{High: 0.8, Low: 0.5, AutoMerge: true, LinkBonus: 0.05, MaxPerEvent: 5, DistanceTolM: 50,
			SourcePriority: []string{"probeg", "marathonec"}, DigestAt: "09:30"}
		if err != nil || !reflect.DeepEqual(c, want) {
			t.Fatalf("LoadConfig = %+v, %v", c, err)
		}
	})
	t.Run("bad value", func(t *testing.T) {
		bad := [][]string{ // имя, значение, дополнительные переменные
			{"DEDUP_HIGH", "x9q"}, {"DEDUP_LOW", "x9q"}, {"DEDUP_AUTO_MERGE", "x9q"}, {"DEDUP_LINK_BONUS", "x9q"},
			{"DEDUP_MAX_PER_EVENT", "x9q"}, {"DEDUP_DISTANCE_TOL_M", "x9q"},
			{"DEDUP_HIGH", "1.5"}, {"DEDUP_LOW", "0"}, {"DEDUP_LOW", "0.8", "DEDUP_HIGH", "0.7"},
			{"DEDUP_LINK_BONUS", "-0.1"}, {"DEDUP_LINK_BONUS", "1.5"}, {"DEDUP_MAX_PER_EVENT", "0"},
			{"DEDUP_DISTANCE_TOL_M", "-1"}, {"DEDUP_DISTANCE_TOL_M", "5000"},
			{"DEDUP_SOURCE_PRIORITY", " , "}, {"DEDUP_SOURCE_PRIORITY", "marathonec,marathonec"},
			{"DEDUP_DIGEST_AT", "25:00"}, {"DEDUP_DIGEST_AT", "x9q"},
		}
		for _, kv := range bad {
			t.Run(kv[0]+"="+kv[1], func(t *testing.T) {
				for i := 0; i < len(kv); i += 2 {
					t.Setenv(kv[i], kv[i+1])
				}
				_, err := LoadConfig()
				if err == nil || !strings.Contains(err.Error(), kv[0]) || strings.Contains(err.Error(), strings.TrimSpace(kv[1])) {
					t.Fatalf("err = %v, want name %s without the value", err, kv[0])
				}
			})
		}
	})
}
