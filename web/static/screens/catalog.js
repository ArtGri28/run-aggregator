// Каталог: поле текстового поиска, чипы направлений, активные фильтры,
// страница выдачи GET /events по сохранённым фильтрам; кнопка «Ещё»
// подгружает следующую страницу. Запрос от двух символов заменяет выдачу
// результатами GET /search: поиск идёт по названию и городу всех будущих
// стартов страны фильтров, остальные фильтры на него не действуют.

import { get, meta, search } from '../api.js';
import { el, events, heading, showError } from '../ui.js';
import { glyph } from '../icons.js';
import { row } from '../row.js';
import { directions, filterTags, loadFilters, resetFilters, saveFilters, toQuery } from './filters.js';

export const back = '#/';

const MIN_QUERY = 2;
// Пауза после последнего символа перед автоматическим поиском.
const TYPING_MS = 450;

// clean — запрос одной строкой без лишних пробелов.
function clean(text) {
  return String(text || '').trim().replace(/\s+/g, ' ');
}

// catalogHash — адрес каталога с запросом (URL-кодированным) или без него.
// Запрос живёт только в адресе: по нему «Назад» из карточки возвращает к
// результатам; в хранилище он не пишется.
export function catalogHash(text) {
  const q = clean(text);
  return q ? '#/catalog?q=' + encodeURIComponent(q) : '#/catalog';
}

// decode — запрос из параметра маршрута; негодная кодировка — пусто.
function decode(param) {
  try { return clean(decodeURIComponent(param || '')); } catch { return ''; }
}

export async function render(box, param) {
  box.replaceChildren();
  let m;
  try {
    m = await meta();
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box, param));
    return;
  }
  if (!box.isConnected) return;
  const f = loadFilters();
  const line = el('p', { class: 'hint', role: 'status' });
  const list = el('ul', { class: 'list list-2' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', { class: 'field' });
  const tags = el('div', {});
  const cities = el('div', { class: 'field', hidden: true });

  let page = 1;
  let seq = 0;
  async function load() {
    const my = seq;
    more.disabled = true;
    try {
      const data = await get('events', { ...toQuery(f), page });
      if (my !== seq || !box.isConnected) return;
      line.textContent = `Найдено ${events(data.total)}`;
      if (page === 1 && !data.items.length) list.append(el('li', { class: 'notice' }, el('p', { text: 'По этим фильтрам стартов пока нет.' })));
      for (const it of data.items) list.append(row(it, m));
      page = data.page + 1;
      more.hidden = data.page >= data.pages;
    } catch (e) {
      if (my === seq && box.isConnected) showError(foot, e, load);
    } finally {
      more.disabled = false;
    }
  }
  // reload — выдача с первой страницы после смены направления.
  function reload() {
    seq++;
    page = 1;
    more.hidden = true;
    list.replaceChildren();
    list.hidden = false;
    foot.replaceChildren();
    tags.replaceChildren(filterTags(f, m));
    load();
  }

  // Поиск: прежний запрос отменяется, ответ на устаревший — не показывается.
  // Упрощение: без постраничной выдачи — показаны первые старты ответа; при
  // необходимости — параметр page у GET /search.
  let pending = null;
  async function find(q) {
    const my = ++seq;
    if (pending) pending.abort();
    pending = new AbortController();
    more.hidden = true;
    try {
      const data = await search(q, toQuery(f).country, pending.signal);
      if (my !== seq || !box.isConnected) return;
      foot.replaceChildren();
      list.replaceChildren(...data.items.map((it) => row(it, m)));
      list.hidden = !data.items.length;
      line.textContent = data.total ? `Найдено ${events(data.total)}` : 'Ничего не нашлось';
      if (data.total > data.items.length) {
        foot.append(el('p', { class: 'hint', text: `Показаны первые ${data.items.length} — уточните запрос.` }));
      }
      if (!data.total) {
        foot.append(el('p', { class: 'hint', text: 'Попробуйте другое слово или сбросьте запрос.' }),
          el('button', { type: 'button', class: 'btn btn-block', text: 'Сбросить запрос', onclick: () => { input.value = ''; apply(); } }));
      }
      cities.replaceChildren();
      cities.hidden = !data.cities.length;
      if (data.cities.length) {
        cities.append(el('h3', { text: 'Города' }), el('div', { class: 'chips' }, ...data.cities.map((c) => el('button', {
          type: 'button', class: 'chip', onclick: () => pick(c),
        }, glyph('pin'), `${c.name} · ${c.count}`))));
      }
    } catch (e) {
      if (my !== seq || !box.isConnected) return;
      foot.replaceChildren();
      showError(foot, e, () => find(q));
    }
  }
  // pick — город из подсказок становится фильтром города в стране фильтров;
  // запрос снимается, возвращается обычная выдача.
  function pick(c) {
    Object.assign(f, { city: c.key, cityName: c.name });
    saveFilters(f);
    input.value = '';
    apply();
  }

  // apply — показывает то, что набрано: от MIN_QUERY символов — результаты
  // поиска, иначе обычную выдачу. shown — запрос, который уже на экране.
  let shown = null;
  let timer = 0;
  function apply() {
    clearTimeout(timer);
    clear.hidden = !input.value;
    let q = clean(input.value);
    if (q.length < MIN_QUERY) q = '';
    if (q === shown) return;
    shown = q;
    // Адрес меняется без перехода: экран не перерисовывается.
    window.history.replaceState(null, '', catalogHash(q));
    dirs.hidden = !!q;
    tags.hidden = !!q;
    if (q) {
      find(q);
      return;
    }
    if (pending) pending.abort();
    cities.hidden = true;
    reload();
  }

  const input = el('input', {
    type: 'search', placeholder: 'Название или город…', 'aria-label': 'Поиск стартов',
    enterkeyhint: 'search', autocomplete: 'off', maxlength: '64',
    oninput: () => {
      clear.hidden = !input.value;
      clearTimeout(timer);
      timer = setTimeout(apply, TYPING_MS);
    },
  });
  input.value = decode(param);
  const clear = el('button', {
    type: 'button', class: 'clear', 'aria-label': 'Очистить запрос', text: '✕',
    onclick: () => { input.value = ''; apply(); input.focus(); },
  });
  const form = el('form', {
    class: 'search', role: 'search',
    onsubmit: (e) => { e.preventDefault(); apply(); input.blur(); },
  }, el('div', { class: 'box' }, glyph('search'), input, clear), el('button', { type: 'submit', class: 'btn btn-primary', text: 'Найти' }));
  const dirs = directions(m, f, reload);

  box.append(heading('Открой новое', 'Каталог стартов',
    el('a', { class: 'pill', href: '#/filters' }, glyph('sliders'), 'Фильтры'),
    el('button', {
      type: 'button', class: 'pill', text: 'Сбросить',
      onclick: () => { clearTimeout(timer); if (pending) pending.abort(); resetFilters(); window.history.replaceState(null, '', '#/catalog'); render(box); },
    })),
  form, dirs, tags, cities, line, list, foot, more);
  apply();
}
