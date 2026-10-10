package bot

import (
	"regexp"
	"strings"

	"aggregator/internal/event"
)

// areaWordRE — слово административной единицы (район, область, край, округ,
// республика) как отдельное слово: у границ слова — не буква.
var areaWordRE = regexp.MustCompile(`(?i)(^|[^\p{L}])(район|р-н|р-он|область|обл|край|округ|республика|респ)($|[^\p{L}])`)

// VisibleAddress — часть events.address, которую показывает бот: без частей,
// состоящих только из района или области (пользователь видит город, а не
// административное деление), и без названия области события. Улица, дом и
// другие уточнения остаются. В БД адрес не меняется.
func VisibleAddress(e event.Event) string {
	var keep []string
	for _, p := range strings.Split(e.Address, ",") {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case e.Region != "" && strings.EqualFold(p, e.Region):
		case areaWordRE.MatchString(p) && !strings.ContainsAny(p, "0123456789"):
		default:
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ", ")
}
