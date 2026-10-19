package normalize

import (
	"testing"

	"aggregator/internal/event"
)

func TestSportTypeWords(t *testing.T) {
	for _, c := range []struct {
		sport event.Sport
		label string
		want  event.Type
	}{
		{event.SportSwimming, "Заплыв на открытой воде", event.TypeOpenWater},
		{event.SportSwimming, "X-WATERS Волга 2027", event.TypeOpenWater},
		{event.SportSwimming, "Кубок, бассейн 50 м", event.TypePool},
		{event.SportSwimming, "Минуты в бассейне", event.TypePool},
		{event.SportSwimming, "SWIMCUP Индор, бассейн", event.TypeOpenWater},
		{event.SportSwimming, "Босфор", event.TypeOther},
		{event.SportSwimming, "Бассейновая", event.TypePool},
		{event.SportCycling, "Гранфондо Москва", event.TypeRoadBike},
		{event.SportCycling, "Шоссейная гонка", event.TypeRoadBike},
		{event.SportCycling, "Жук трейл Логойск хсм", event.TypeMTB},
		{event.SportCycling, "Жук-трейл [МТБ]", event.TypeMTB},
		{event.SportCycling, "XCO Cup", event.TypeMTB},
		{event.SportCycling, "Грэвел 100", event.TypeGravel},
		{event.SportCycling, "Гревёл-рейд", event.TypeGravel},
		{event.SportCycling, "Samba Trail: Арчеда VI, Fat bike - 50 км, Вело - 28 км", event.TypeOther},
		{event.SportCycling, "хсмный", event.TypeOther},
		{event.SportRunning, "МТБ", event.TypeOther},
		{event.SportSkiing, "бассейн", event.TypeOther},
	} {
		if got := SportType(c.sport, c.label); got != c.want {
			t.Errorf("SportType(%s, %q) = %q, want %q", c.sport, c.label, got, c.want)
		}
	}
}
