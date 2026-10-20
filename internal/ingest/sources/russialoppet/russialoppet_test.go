package russialoppet

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"aggregator/internal/ingest"
)

// fixtureNow — день загрузки образца (testdata/README.md).
var fixtureNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func TestParseFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/events-2027.html")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Parse(ingest.RawDoc{URL: Site + "/events/2027/", Body: body, FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("testdata/events-2027.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []ingest.SourceEvent
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, want) {
		g, _ := json.MarshalIndent(recs, "", "  ")
		t.Fatalf("записи не совпадают с эталоном:\n%s", g)
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || len(prep) != len(recs) || st.UnrecognizedRegion != 0 {
		t.Fatalf("Prepare: %d из %d, отбраковано %d, нераспознанных областей %d (%v)", len(prep), len(recs), st.Rejected, st.UnrecognizedRegion, st.Unrecognized)
	}
}

func row(id, href, title, reg, date, district, place, dist string) string {
	r := ""
	if reg != "" {
		r = `<a class="button read_more" href="` + reg + `">Зарегистрироваться</a>`
	}
	return `<tr class="item" id="bx_1_` + id + `"><td class="foto-cell"></td>` +
		`<td class="item-name-cell"><a href="` + href + `">` + title + `</a></td>` +
		`<td class="but-reg">` + r + `</td>` +
		`<td class="but-cell item_` + id + `"><span class="date">` + date + `</span><br>` +
		`<span><a href="?region=1">Центральный ФО</a></span>, <span><a href="?district=">` + district + `</a></span>, <span>` + place + `</span>` +
		`<br>` + dist + `</td></tr>`
}

func parse(t *testing.T, rows ...string) []ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(ingest.RawDoc{Body: []byte(`<table><tbody>` + strings.Join(rows, "") + `</tbody></table>`)})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestParseEdge(t *testing.T) {
	recs := parse(t,
		row("1", "/events/a.html", "Через месяц", "/registration/a/", "28.02-01.03.2027", "Тульская обл.", "Тула", `<b>50</b>/25/1/600 м/5 км  <b>КЛ</b>`),
		row("2", "/events/b.html", "Без области", "", "21.02.2027", "", "Тамбовская область, Котовск", `<b>30</b> км  <b>СВ</b>`),
		row("3", "/events/c.html", "Награждение", "/registration/c/", "11.11.2026", "Москва", "Москва, БСА Лужники", `<b></b> км  <b></b>`),
		row("4", "/events/d.html", "Без даты", "", "уточняется", "Москва", "Москва", `<b>50</b> км`),
		row("5", "/events/e.html", "Республика", "", "07.03.2027", "Р. Удмуртская", "п. Игра", `<b>10</b>/200 м км <b>СВ</b>`),
	)
	if len(recs) != 3 {
		t.Fatalf("записей %d, want 3: %+v", len(recs), recs)
	}
	a, b, e := recs[0], recs[1], recs[2]
	if a.Date != "28.02.2027" || a.EndDate != "01.03.2027" || a.Distances != "50 км, 25 км, 1 км, 600 м, 5 км" ||
		a.RegistrationURL != Site+"/registration/a/" || a.URL != Site+"/events/a.html" || a.ExternalID != "1" || a.Region != "Тульская обл." {
		t.Errorf("диапазон: %+v", a)
	}
	if b.Region != "Тамбовская область" || b.City != "Котовск" || b.RegistrationURL != "" || b.EndDate != "" {
		t.Errorf("область в поле города: %+v", b)
	}
	if e.Region != "Удмуртская Республика" || e.City != "Игра" || e.Address != "п. Игра" || e.Distances != "10 км, 200 м" || e.Sport != "skiing" || e.Country != "RU" {
		t.Errorf("республика: %+v", e)
	}
	if _, err := Parse(ingest.RawDoc{Body: []byte(`<html><body><p>нет</p></body></html>`)}); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Errorf("без таблицы: %v", err)
	}
}

func TestSeason(t *testing.T) {
	for _, c := range []struct {
		now  time.Time
		want int
	}{
		{time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), 2027},
		{time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC), 2027},
		{time.Date(2027, 4, 30, 0, 0, 0, 0, time.UTC), 2027},
		{time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC), 2028},
	} {
		if got := Season(c.now); got != c.want {
			t.Errorf("Season(%v) = %d, want %d", c.now, got, c.want)
		}
	}
}

// TestFetchTLS — клиент с TLSConfig не принимает сервер с RSA-сертификатом
// и забирает календарь сезона у сервера с ECDSA-сертификатом; WithTLS не
// меняет исходный загрузчик.
func TestFetchTLS(t *testing.T) {
	var path string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; w.Write([]byte("<table></table>")) })
	rsa := httptest.NewTLSServer(h)
	defer rsa.Close()
	ec := httptest.NewUnstartedServer(h)
	ec.TLS = &tls.Config{Certificates: []tls.Certificate{ecdsaCert(t)}}
	ec.StartTLS()
	defer ec.Close()

	roots := x509.NewCertPool()
	roots.AddCert(rsa.Certificate())
	roots.AddCert(ec.Certificate())
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent, cfg.Attempts = "test", 1
	cfg.TLS = &tls.Config{RootCAs: roots}
	base := ingest.NewFetcher(cfg)
	tc := TLSConfig()
	tc.RootCAs = roots
	src := func(site string) *Source {
		return &Source{f: base.WithTLS(tc), site: site, now: func() time.Time { return fixtureNow }}
	}

	if _, err := src(rsa.URL).Fetch(context.Background()); err == nil {
		t.Fatal("сервер с RSA-сертификатом принят")
	}
	if _, err := base.Get(context.Background(), rsa.URL+"/x", 0); err != nil {
		t.Fatalf("исходный загрузчик изменился: %v", err)
	}
	docs, err := src(ec.URL).Fetch(context.Background())
	if err != nil || len(docs) != 1 || path != "/events/2027/" {
		t.Fatalf("ECDSA: %v, документов %d, путь %q", err, len(docs), path)
	}
	if New(nil).f != nil {
		t.Error("New(nil): загрузчик не nil")
	}
}

// ecdsaCert — самоподписанный ECDSA-сертификат для 127.0.0.1.
func ecdsaCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
