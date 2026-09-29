# API Coverage — marathonec.ru (первый источник Phase 3)

> Full coverage by default. Opt-outs are explicit, reasoned decisions.

Официального API у источника нет (01-SOURCES-REPORT §2.1 п. 4); поверхность — публичные страницы сайта. Матрица фиксирует, что из неё используется.

| capability | decision | reason |
|---|---|---|
| calendar-list-page | INTEGRATE | |
| list-row-fields | INTEGRATE | |
| external-registration-links | INTEGRATE | |
| event-detail-pages | OPT-OUT | not needed: поля MVP есть в строке списка; 31 страница при Crawl-delay 40 с заняла бы около 21 мин прогона (01-SOURCES-REPORT §5 п. 3) |
| wp-rest-api | OPT-OUT | not needed: не отдаёт дату забега, URL с `?` закрыты `Disallow: /*?*` (01-SOURCES-REPORT §2.1 п. 4) |
| sitemap | OPT-OUT | not needed: одна страница списка содержит все строки, 1 запрос за прогон |
| rss-feed | OPT-OUT | explicitly out of scope: `/feed` закрыт в robots.txt |
| json-ld | OPT-OUT | not needed: решение D-10 — разбор HTML-таблицы; типы схем фиксируются в testdata/README.md (план 03-01) |
| conditional-get | OPT-OUT | not available: сайт не отдаёт ETag и Last-Modified (`cache-control: max-age=0`) |
