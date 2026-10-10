package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aggregator/internal/bot"
	"aggregator/internal/event"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
	"aggregator/web"
)

// API Mini App (Phase 15). Чтение каталога (этап 2): meta, events,
// events/{id}, cities, cities/suggest; календарь (этап 5): calendar. По
// пользователю (этап 4): favorites (чтение и запись) и profile (только чтение);
// пользователь — только TgID
// из проверенного initData, в пути, параметрах и теле его нет. Единственная
// таблица, в которую пишет этот пакет, — favorites (D-04, D-10). Все маршруты —
// за Middleware; SQL здесь нет, всё через *store.Store. Контракты ответов —
// раздел 5 плана. Статика Mini App (этап 3) отдаётся из web.FS на всех
// остальных путях без проверки initData: он нужен только API.

const (
	// defaultLimit и maxLimit — размер страницы по умолчанию и предел (D-11).
	defaultLimit = 20
	maxLimit     = 50
	// maxCalendarDays — предел диапазона календаря в днях, включая оба края.
	maxCalendarDays = 62
	// rateLimit запросов чтения и writeLimit запросов записи за rateWindow на
	// пользователя → 429 (D-11, раздел 5 плана); счётчики раздельные.
	rateLimit  = 60
	writeLimit = 20
	rateWindow = time.Minute
	// sweepAt — число пользователей в счётчике, при котором устаревшие окна
	// вычищаются перед добавлением нового.
	sweepAt = 1024
	// maxBody — предел тела запроса: API тела не ждёт вовсе и не читает его.
	maxBody = 64 << 10
	// cacheControl — ответы чтения каталога кэшируются только у клиента.
	cacheControl = "private, max-age=60"
	// noStore — ответы по пользователю (избранное, профиль, карточка с
	// состоянием избранного) и ответы записи не кэшируются.
	noStore = "no-store"
	// shutdownGrace — предел ожидания текущих запросов при остановке.
	shutdownGrace = 10 * time.Second
	// staticCacheControl — статика перепроверяется при каждом открытии.
	// Упрощение: без хеша версии в имени файла; при росте трафика — хеш в
	// имени и долгий max-age.
	staticCacheControl = "no-cache"
	// csp — политика содержимого страницы: свои файлы, скрипт Telegram,
	// запросы только к своему origin, без inline-скриптов и стилей.
	csp = "default-src 'self'; script-src 'self' https://telegram.org; connect-src 'self'; img-src 'self' data:; style-src 'self'"
)

var (
	// cityKeyRE и countryRE — грамматика ключа города и кода страны, как у
	// кодека бота.
	cityKeyRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	countryRE = regexp.MustCompile(`^[A-Z]{2}$`)
	// idRE — идентификатор события в пути: десятичное число без знака и
	// ведущих нулей.
	idRE = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	// moscow — часовой пояс «сегодня» (как у бота: календарная дата по
	// Москве); перехода на летнее время там нет с 2014 года.
	moscow = time.FixedZone("MSK", 3*60*60)
	// errBadRequest — неверный параметр запроса → 400 bad_request.
	errBadRequest = errors.New("bad_request")
)

type server struct {
	store *store.Store
	dict  bot.Dictionary
	now   func() time.Time
	log   *slog.Logger
	limit *limiter
	write *limiter
}

// New собирает маршруты API. Каждый маршрут: лог → проверка initData →
// ограничение частоты (у записи — свой, более строгий счётчик) → обработчик;
// неизвестный путь под /api/ — 404 JSON (тоже за проверкой).
func New(s *store.Store, secret []byte, now func() time.Time, log *slog.Logger) http.Handler {
	srv := &server{
		store: s, dict: bot.Catalog(), now: now, log: log,
		limit: &limiter{now: now, max: rateLimit, hits: map[int64]*window{}},
		write: &limiter{now: now, max: writeLimit, hits: map[int64]*window{}},
	}
	auth := Middleware(secret, now, func(code string) { log.Info("auth rejected", "code", code) })
	mux := http.NewServeMux()
	handle := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, srv.logged(auth(limited(srv.limit, h))))
	}
	handleWrite := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, srv.logged(auth(limited(srv.write, h))))
	}
	handle("GET /api/v1/meta", srv.meta)
	handle("GET /api/v1/events", srv.events)
	handle("GET /api/v1/events/{id}", srv.event)
	handle("GET /api/v1/calendar", srv.calendar)
	handle("GET /api/v1/cities", srv.cities)
	handle("GET /api/v1/cities/suggest", srv.suggest)
	handle("GET /api/v1/favorites", srv.favorites)
	handleWrite("PUT /api/v1/favorites/{id}", srv.favoriteAdd)
	handleWrite("DELETE /api/v1/favorites/{id}", srv.favoriteRemove)
	handle("GET /api/v1/profile", srv.profile)
	handle("/api/", func(w http.ResponseWriter, _ *http.Request) { fail(w, http.StatusNotFound, "not_found") })
	mux.Handle("/", srv.logged(static()))
	return mux
}

// static — файлы Mini App из web.FS; путь без файла (маршруты hash-роутера,
// корень) → index.html. Заголовки защиты и Cache-Control — на каждом ответе.
func static() http.Handler {
	files, err := fs.Sub(web.FS, "static")
	if err != nil {
		panic(err) // каталог встроен на этапе сборки
	}
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", staticCacheControl)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if st, err := fs.Stat(files, name); name == "" || err != nil || st.IsDir() {
			http.ServeFileFS(w, r, files, "index.html")
			return
		}
		server.ServeHTTP(w, r)
	})
}

// NewServer — http.Server с таймаутами: медленный клиент не держит
// соединение и воркер.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr: addr, Handler: h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Shutdown останавливает сервер, дожидаясь текущих запросов не дольше
// shutdownGrace.
func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(ctx)
}

// statusWriter запоминает код ответа для лога.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// logged — запись в лог: метод, шаблон маршрута, код, длительность (D-12);
// без параметров запроса, заголовков и пользователя. Тело ограничено maxBody.
func (s *server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		r.Body = http.MaxBytesReader(sw, r.Body, maxBody)
		next.ServeHTTP(sw, r)
		s.log.Info("http", "method", r.Method, "route", r.Pattern, "status", sw.code, "ms", time.Since(start).Milliseconds())
	})
}

// limiter — счётчик запросов пользователя в фиксированном окне rateWindow с
// пределом max.
// Упрощение: в памяти одного процесса; при нескольких экземплярах webapp —
// счётчик в базе или на прокси.
type limiter struct {
	now  func() time.Time
	max  int
	mu   sync.Mutex
	hits map[int64]*window
}

type window struct {
	start time.Time
	n     int
}

// take учитывает запрос пользователя; результат — секунды до конца окна,
// если предел превышен, иначе 0.
func (l *limiter) take(id int64) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.hits[id]
	if w == nil || now.Sub(w.start) >= rateWindow {
		if len(l.hits) >= sweepAt {
			for k, v := range l.hits {
				if now.Sub(v.start) >= rateWindow {
					delete(l.hits, k)
				}
			}
		}
		w = &window{start: now}
		l.hits[id] = w
	}
	w.n++
	if w.n > l.max {
		return int(math.Ceil((rateWindow - now.Sub(w.start)).Seconds()))
	}
	return 0
}

// limited — 429 с Retry-After при превышении предела пользователя в счётчике l.
func limited(l *limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := TgID(r.Context())
		if retry := l.take(id); retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			fail(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Обработчик ответа по пользователю ставит noStore сам, до вызова.
	if status == http.StatusOK && w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail — ответ-ошибка {"error":code}.
func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// internal — ошибка базы: в лог без текста ответа клиенту (как errNotice бота).
func (s *server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("store", "route", r.Pattern, "err", err)
	fail(w, http.StatusInternalServerError, "internal")
}

// today — сегодняшняя календарная дата по Москве как полночь UTC (так
// хранятся start_date).
func (s *server) today() time.Time {
	y, m, d := s.now().In(moscow).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// countryParam — страна запроса: пусто — DefaultCountry, "*" — все страны
// (для store — пустая строка), иначе код ISO alpha-2.
func (s *server) countryParam(q url.Values) (code string, all bool, err error) {
	c := q.Get("country")
	switch {
	case c == "":
		return s.dict.DefaultCountry, false, nil
	case c == "*":
		return "", true, nil
	case countryRE.MatchString(c):
		return c, false, nil
	}
	return "", false, errBadRequest
}

// filterQuery разбирает параметры каталога в event.Filter: sport, country,
// city (только со страной), type, dist (индексы корзин вида спорта),
// periods либо from/to. From не раньше «сегодня».
func (s *server) filterQuery(q url.Values, today time.Time) (event.Filter, error) {
	f := event.Filter{From: today}
	if sp := q.Get("sport"); sp != "" {
		info, ok := event.SportByCode(event.Sport(sp))
		if !ok || !info.Enabled {
			return f, errBadRequest
		}
		f.Sport = info.Code
	}
	code, all, err := s.countryParam(q)
	if err != nil {
		return f, err
	}
	f.CountryCode = code
	if city := q.Get("city"); city != "" {
		if all || len(city) > 60 || !cityKeyRE.MatchString(city) {
			return f, errBadRequest
		}
		f.CityKey = city
	}
	if t := q.Get("type"); t != "" {
		if !typeAllowed(f.Sport, event.Type(t)) {
			return f, errBadRequest
		}
		f.Type = event.Type(t)
	}
	if dist := q.Get("dist"); dist != "" {
		bs := s.dict.Buckets[f.Sport] // без вида спорта корзин нет
		for _, p := range strings.Split(dist, ",") {
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(bs) {
				return f, errBadRequest
			}
			f.Distances = append(f.Distances, event.DistanceRange{Lo: bs[i].Lo, Hi: bs[i].Hi})
		}
	}
	periods, from, to := q.Get("periods"), q.Get("from"), q.Get("to")
	if periods != "" && (from != "" || to != "") {
		return f, errBadRequest
	}
	if periods != "" {
		for _, p := range strings.Split(periods, ",") {
			if !s.dict.ValidPeriod(p) {
				return f, errBadRequest
			}
			lo, hi := s.dict.DateRange(p, today)
			f.Periods = append(f.Periods, event.DateRange{From: lo, To: hi})
		}
	}
	if from != "" {
		d, err := time.Parse(time.DateOnly, from)
		if err != nil {
			return f, errBadRequest
		}
		if d.After(today) {
			f.From = d
		}
	}
	if to != "" {
		d, err := time.Parse(time.DateOnly, to)
		if err != nil {
			return f, errBadRequest
		}
		f.To = d
	}
	return f, nil
}

// typeAllowed — дисциплина t допустима у вида спорта sp; sp пустой — у
// любого включённого.
func typeAllowed(sp event.Sport, t event.Type) bool {
	if sp != "" {
		return slices.Contains(event.SportTypes[sp], t)
	}
	for _, info := range enabledSports() {
		if slices.Contains(event.SportTypes[info.Code], t) {
			return true
		}
	}
	return false
}

// intParam — целое из [1, max]; пусто — def.
func intParam(q url.Values, key string, def, max int) (int, error) {
	v := q.Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > max {
		return 0, errBadRequest
	}
	return n, nil
}

// pathID — идентификатор события из пути: десятичное число больше нуля без
// знака и ведущих нулей.
func pathID(r *http.Request) (int64, bool) {
	v := r.PathValue("id")
	if !idRE.MatchString(v) {
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	return id, err == nil
}

// meta — справочники и счётчики: события по включённым видам спорта (во
// всех странах, как menuSports бота) и страны каталога.
func (s *server) meta(w http.ResponseWriter, r *http.Request) {
	ctx, today := r.Context(), s.today()
	m := dictJSON(s.dict, today)
	for i := range m.Sports {
		n, err := s.store.CountEvents(ctx, event.Filter{From: today, Sport: m.Sports[i].Code})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		m.Sports[i].Count = n
	}
	countries, err := s.store.Countries(ctx, event.Filter{From: today})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	m.Countries = []countryJSON{}
	for _, c := range countries {
		m.Countries = append(m.Countries, countryJSON{c.Code, name(s.dict.CountryNames, c.Code), c.N})
	}
	writeJSON(w, http.StatusOK, m)
}

// events — страница выдачи по фильтру.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	ctx, today, q := r.Context(), s.today(), r.URL.Query()
	f, err := s.filterQuery(q, today)
	page, errPage := intParam(q, "page", 1, s.dict.MaxPage)
	limit, errLimit := intParam(q, "limit", defaultLimit, maxLimit)
	if err != nil || errPage != nil || errLimit != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	total, err := s.store.CountEvents(ctx, f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items, err := s.store.ListEvents(ctx, f, limit, (page-1)*limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, listJSON{Total: total, Page: page, Pages: (total + limit - 1) / limit, Items: itemsJSON(items)})
}

// calendar — число событий выдачи по дням диапазона from…to (оба обязательны,
// не больше maxCalendarDays дней): только дни с событиями. Фильтры и нижняя
// граница «сегодня» — тот же разбор, что у events: счётчик дня равен total
// у events?from=день&to=день.
func (s *server) calendar(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	f, err := s.filterQuery(q, s.today())
	from, errFrom := time.Parse(time.DateOnly, q.Get("from"))
	to, errTo := time.Parse(time.DateOnly, q.Get("to"))
	if err != nil || errFrom != nil || errTo != nil || to.Before(from) || to.Sub(from) > (maxCalendarDays-1)*24*time.Hour {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	days, err := s.store.CountByDay(ctx, f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]dayJSON, 0, len(days))
	for _, d := range days {
		out = append(out, dayJSON{d.Date.Format(time.DateOnly), d.Count})
	}
	writeJSON(w, http.StatusOK, out)
}

// event — карточка корня слияний (RootEventID). Нет события, скрыто, слито
// или вид спорта выключен — 404 gone, как «Событие больше не доступно» у бота.
func (s *server) event(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	// В карточке — состояние избранного пользователя: не кэшируется, иначе
	// после добавления или удаления клиент увидел бы прежнее.
	w.Header().Set("Cache-Control", noStore)
	root, err := s.store.RootEventID(ctx, id)
	var e event.Event
	if err == nil {
		e, err = s.store.GetEvent(ctx, root)
	}
	sp, _ := event.SportByCode(e.Sport) // нет в справочнике — как выключен
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "gone")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	case !e.IsListed || e.MergedIntoID != nil || !sp.Enabled:
		fail(w, http.StatusNotFound, "gone")
		return
	}
	srcs, err := s.store.EventSources(ctx, e.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	tgID, _ := TgID(ctx)
	fav, err := s.store.IsFavorite(ctx, tgID, e.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cardJSONOf(s.dict, e, srcs, fav))
}

// cities — города с наибольшим числом событий по фильтру (сам фильтр города
// не учитывается — как в меню бота).
func (s *server) cities(w http.ResponseWriter, r *http.Request) {
	ctx, today, q := r.Context(), s.today(), r.URL.Query()
	f, err := s.filterQuery(q, today)
	limit, errLimit := intParam(q, "limit", 10, maxLimit)
	if err != nil || errLimit != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	out, err := s.store.TopCities(ctx, f, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, citiesJSON(out))
}

// suggest — подсказки городов по тексту q (не длиннее MaxCityInput рун) в
// стране запроса; текст в лог не попадает.
func (s *server) suggest(w http.ResponseWriter, r *http.Request) {
	ctx, today, q := r.Context(), s.today(), r.URL.Query()
	country, _, err := s.countryParam(q)
	text := strings.Join(strings.Fields(q.Get("q")), " ")
	if err != nil || text == "" || utf8.RuneCountInString(text) > s.dict.MaxCityInput {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	out, err := s.store.SimilarCities(ctx, country, normalize.CityKey(text), text, today, 5)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, citiesJSON(out))
}

// favorites — страница избранного пользователя запроса: только будущие
// видимые старты; запись на слитое событие показывается его корнем (как
// раздел «Избранное» бота). В базу не пишет.
func (s *server) favorites(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	w.Header().Set("Cache-Control", noStore)
	page, errPage := intParam(q, "page", 1, s.dict.MaxPage)
	limit, errLimit := intParam(q, "limit", defaultLimit, maxLimit)
	if errPage != nil || errLimit != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	tgID, _ := TgID(ctx)
	items, total, err := s.store.ListFavorites(ctx, tgID, s.today(), limit, (page-1)*limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, favListJSON{Total: total, Page: page, Pages: (total + limit - 1) / limit, Items: favItemsJSON(items)})
}

// favOutcomes — исход добавления в ответе; FavGone — 404 gone.
var favOutcomes = map[store.FavOutcome]string{
	store.FavAdded:   "added",
	store.FavAlready: "already",
	store.FavLimit:   "limit",
	store.FavPast:    "past",
}

// favoriteAdd добавляет старт в избранное пользователя запроса; повтор —
// "already". Тело запроса не читается. Предел — как у бота (FavLimit).
func (s *server) favoriteAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", noStore)
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	tgID, _ := TgID(ctx)
	res, err := s.store.AddFavorite(ctx, tgID, id, s.today(), s.now(), s.dict.FavLimit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	outcome, ok := favOutcomes[res]
	if !ok {
		fail(w, http.StatusNotFound, "gone")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
}

// favoriteRemove убирает старт из избранного пользователя запроса; нет записи
// или события — тоже 204. Убрать можно и скрытый, и прошедший старт.
func (s *server) favoriteRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", noStore)
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	tgID, _ := TgID(ctx)
	if err := s.store.RemoveFavorite(ctx, tgID, id); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// profile — ответы анкеты пользователя запроса только для чтения
// (PeekProfile строк не создаёт); нет строки — {"exists":false}.
func (s *server) profile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", noStore)
	tgID, _ := TgID(ctx)
	p, _, ok, err := s.store.PeekProfile(ctx, tgID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]bool{"exists": false})
		return
	}
	writeJSON(w, http.StatusOK, profileJSONOf(s.dict, p))
}
