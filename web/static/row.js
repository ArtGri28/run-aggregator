// Строка старта — одна на главную, каталог, календарь и избранное — и
// состояние избранного для сердечка в строке (D-17).

import { del, get, put } from './api.js';
import { el, errorText, km, MON, statusBadge, toast } from './ui.js';
import { glyph, icon } from './icons.js';

// Упрощение: список стартов не отдаёт признак избранного, поэтому
// идентификаторы избранного читаются один раз за сеанс через GET /favorites
// (страницы по 50, не больше FAV_PAGES страниц) и живут в памяти модуля;
// изменения, сделанные в боте во время сеанса, видны после повторного
// открытия. При росте предела избранного — признак в ответе events.
const FAV_PAGES = 10;
let favs = null;

// favIds — множество идентификаторов избранных будущих стартов.
export function favIds() {
  if (!favs) {
    favs = (async () => {
      const ids = new Set();
      for (let page = 1; page <= FAV_PAGES; page++) {
        const data = await get('favorites', { limit: 50, page });
        for (const it of data.items) ids.add(it.id);
        if (page >= data.pages) break;
      }
      return ids;
    })().catch((e) => { favs = null; throw e; });
  }
  return favs;
}

// toggleFav — задаёт состояние избранного (PUT/DELETE идемпотентны).
// Результат — новое состояние и текст сообщения ('' — без сообщения).
export async function toggleFav(id, fav, limit) {
  let next = fav;
  let note = '';
  try {
    if (fav) {
      await del('favorites/' + id);
      next = false;
    } else {
      const { outcome } = await put('favorites/' + id);
      if (outcome === 'limit') note = `В избранном уже ${limit} стартов — это предел. Уберите лишний, чтобы добавить новый.`;
      else if (outcome === 'past') note = 'Старт уже прошёл.';
      else next = true;
    }
  } catch (e) {
    note = errorText(e, 'Не удалось сохранить. Повторите позже.');
  }
  if (favs && next !== fav) {
    const ids = await favs.catch(() => null);
    if (ids) ids[next ? 'add' : 'delete'](id);
  }
  return { fav: next, note };
}

// heart — сердечко строки: контурное или залитое; нажатие сразу меняет
// избранное, отказ — короткое сообщение. known — состояние, если оно уже
// известно (экран избранного); onChange — после смены состояния.
function heart(it, m, known, onChange) {
  let fav = known === true;
  const btn = el('button', { type: 'button', class: 'heart' }, glyph('heart'));
  const paint = () => {
    btn.setAttribute('aria-pressed', String(fav));
    btn.setAttribute('aria-label', fav ? 'Убрать из избранного' : 'В избранное');
  };
  paint();
  if (known === undefined) {
    favIds().then((ids) => { fav = ids.has(it.id); paint(); }).catch(() => { /* сердечко остаётся контурным */ });
  }
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    const was = fav;
    const res = await toggleFav(it.id, fav, m.limits.favorites);
    fav = res.fav;
    paint();
    btn.disabled = false;
    toast(res.note);
    if (onChange && fav !== was) onChange(fav);
  });
  return btn;
}

// row — строка старта: плашка даты, название (у небегового старта — со
// значком вида спорта, как в строках бота), место, дистанции и сердечко.
// Сердечко — сосед ссылки, а не её часть: нажатие на него карточку не открывает.
export function row(it, m, { fav, onChange } = {}) {
  const [y, mo, d] = it.start_date.split('-').map(Number);
  const country = (m.countries.find((c) => c.code === it.country_code) || {}).name || it.country_code;
  const place = [it.city, country].filter(Boolean).join(', ');
  const dist = it.distances || [];
  const badge = statusBadge(it.status);
  return el('li', { class: 'ev' },
    el('a', { class: 'ev-link', href: '#/event/' + it.id },
      el('span', { class: 'date' },
        el('b', { text: String(d) }), el('span', { class: 'mon', text: MON[mo - 1] }), el('span', { class: 'year', text: String(y) })),
      el('span', { class: 'body' },
        el('span', { class: 'name' }, it.sport !== 'running' ? icon(it.sport, '') : null, badge, badge ? ' ' : null, it.title),
        place ? el('span', { class: 'place' }, glyph('pin'), el('span', { text: place })) : null,
        dist.length ? el('span', { class: 'tags' }, ...dist.map((x) => el('span', { class: 'tag', text: km(x) + ' км' }))) : null)),
    heart(it, m, fav, onChange));
}
