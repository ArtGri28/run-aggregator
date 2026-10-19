package bot

import (
	"reflect"
	"testing"

	"aggregator/internal/event"
)

// Корзины плавания и велоспорта: граница в верхней корзине, каждое число
// метров — ровно в одной; размер набора — как у бега (кодек f:n).
func TestSwimCycleBuckets(t *testing.T) {
	for _, c := range []struct {
		sport event.Sport
		want  map[int]int
	}{
		{event.SportSwimming, map[int]int{1: 0, 999: 0, 1000: 1, 2999: 1, 3000: 2, 9999: 2, 10000: 3, 25000: 3}},
		{event.SportCycling, map[int]int{1: 0, 49999: 0, 50000: 1, 99999: 1, 100000: 2, 149999: 2, 150000: 3, 300000: 3}},
	} {
		bs := sportBuckets(c.sport)
		in := func(m int) []int {
			var out []int
			for i, bk := range bs {
				if m > bk.lo && m <= bk.hi {
					out = append(out, i)
				}
			}
			return out
		}
		for m := 1; m <= 400000; m++ {
			if len(in(m)) != 1 {
				t.Fatalf("%s: %d м попадает в корзины %v, want ровно одну", c.sport, m, in(m))
			}
		}
		for m, want := range c.want {
			if got := in(m); !reflect.DeepEqual(got, []int{want}) {
				t.Errorf("%s: %d м — корзины %v, want %d", c.sport, m, got, want)
			}
		}
		if len(bs) != len(buckets) {
			t.Errorf("%s: корзин %d, у бега %d", c.sport, len(bs), len(buckets))
		}
	}
	for _, typ := range append(event.SportTypes[event.SportSwimming], event.SportTypes[event.SportCycling]...) {
		if typeLabel(string(typ)) == "" || (typ != event.TypeOther && typeEmoji(typ) == "🏃") {
			t.Errorf("%s: нет подписи или значка", typ)
		}
	}
}
