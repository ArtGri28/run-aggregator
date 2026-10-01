package dedup

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"aggregator/internal/normalize"
)

// NameSim — схожесть названий (DDP-02): триграммный Jaccard по рунам над
// simKey(normalize.TitleNorm(x)). Симметрична, в [0, 1]; пустой ключ у
// любой стороны (название целиком из года, номера, стоп-слов) → 0.
// title_norm в БД не меняется: simKey — своя форма поверх него.
func NameSim(a, b string) float64 {
	ka, kb := simKey(normalize.TitleNorm(a)), simKey(normalize.TitleNorm(b))
	if ka == "" || kb == "" {
		return 0
	}
	ga, gb := grams(ka), grams(kb)
	in := 0
	for g := range ga {
		if _, ok := gb[g]; ok {
			in++
		}
	}
	return float64(in) / float64(len(ga)+len(gb)-in)
}

// simKey убирает из title_norm слова только из цифр, однобуквенные слова и
// короткий суффикс порядкового числа («55 й», «2 я», «5 ый»). Стоп-слова
// сверх TitleNorm не добавляются: «марафон»/«полумарафон» решает
// калибровка (Pitfall 8).
func simKey(titleNorm string) string {
	words := strings.Fields(titleNorm)
	var out []string
	for i, w := range words {
		n := utf8.RuneCountInString(w)
		if isNumber(w) || n == 1 || n <= 2 && i > 0 && isNumber(words[i-1]) {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

func isNumber(w string) bool {
	return w != "" && strings.IndexFunc(w, func(r rune) bool { return !unicode.IsDigit(r) }) < 0
}

// grams — множество триграмм по рунам (не байтам: байты ломают
// кириллицу) над «  »+s+« ».
func grams(s string) map[string]struct{} {
	r := []rune("  " + s + " ")
	m := make(map[string]struct{}, len(r))
	for i := 0; i+3 <= len(r); i++ {
		m[string(r[i:i+3])] = struct{}{}
	}
	return m
}

// socialHosts — страницы соцсетей общие для многих событий организатора:
// совпадение такой ссылки не сигнал.
var socialHosts = []string{"vk.com", "vk.ru", "t.me", "telegram.me", "ok.ru", "instagram.com", "facebook.com", "youtube.com", "youtu.be"}

// LinkKey — хост+путь ссылки регистрации для сравнения: только http(s),
// хост в нижнем регистре без «www.», путь без завершающего «/». "" — не
// сигнал: ошибка разбора, пустой путь, «?» или «#» (трекинг, якоря),
// соцсети. Ссылка — только добавка к схожести названий, сама пару не
// создаёт (DDP-04).
func LinkKey(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if strings.ContainsAny(raw, "?#") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	path := strings.TrimRight(u.Path, "/")
	if host == "" || path == "" {
		return ""
	}
	for _, s := range socialHosts {
		if host == s || strings.HasSuffix(host, "."+s) {
			return ""
		}
	}
	return host + path
}
