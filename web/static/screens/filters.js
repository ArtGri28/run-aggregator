// Экран фильтров и состояние фильтров каталога. Состояние живёт только на
// клиенте (D-10) под одним ключом localStorage; в базу не пишется. Это
// единственный модуль, который обращается к localStorage. Здесь же ряд
// направлений (вид спорта и дисциплина) для главной и каталога.

import { get, meta } from '../api.js';
import { el, flag, heading, showError } from '../ui.js';
import { glyph, icon } from '../icons.js';

const KEY = 'gde-start.filters.v1';
export const ALL_COUNTRIES = '*';
// Упрощение: страна по умолчанию продублирована на клиенте (у сервера —
// DefaultCountry бота); при её смене — отдать в meta.
const DEFAULT_COUNTRY = 'RU';

const DEFAULTS = { sport: '', country: DEFAULT_COUNTRY, city: '', cityName: '', type: '', dist: [], periods: [] };

export const back = '#/catalog';

export function loadFilters() {
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem(KEY) || '{}') || {}; } catch { saved = {}; }
  const f = { ...DEFAULTS, ...saved };
  f.dist = Array.isArray(f.dist) ? f.dist.map(Number).filter(Number.isInteger) : [];
  f.periods = Array.isArray(f.periods) ? f.periods.map(String).filter((p) => p !== 'cm' && p !== 'nm') : [];
  return f;
}

export function saveFilters(f) {
  try { localStorage.setItem(KEY, JSON.stringify(f)); } catch { /* хранилище недоступно — фильтры живут до перезагрузки */ }
  // Пилюля страны в шапке показывает то же состояние.
  window.dispatchEvent(new Event('filters'));
}

// resetFilters — фильтры по умолчанию.
export function resetFilters() {
  saveFilters({ ...DEFAULTS, dist: [], periods: [] });
}

// toQuery — параметры GET /events и /cities по контракту: город только со
// страной, корзины и дисциплина только с видом спорта.
export function toQuery(f) {
  const q = { country: f.country || DEFAULT_COUNTRY };
  if (f.sport) q.sport = f.sport;
  if (f.city && q.country !== ALL_COUNTRIES) q.city = f.city;
  if (f.sport && f.type) q.type = f.type;
  if (f.sport && f.dist.length) q.dist = f.dist.join(',');
  if (f.periods.length) q.periods = f.periods.join(',');
  return q;
}

// parts — подписи активных фильтров по справочникам meta.
function parts(f, m) {
  const out = [];
  const sport = m.sports.find((s) => s.code === f.sport);
  if (sport) out.push(sport.title);
  if (f.country === ALL_COUNTRIES) {
    out.push('все страны');
  } else {
    const c = m.countries.find((x) => x.code === f.country);
    out.push(c ? c.name : f.country);
    if (f.city) out.push(f.cityName || f.city);
  }
  if (f.periods.length) {
    out.push(f.periods.map((p) => (m.months.find((x) => x.code === p) || {}).label || p).join(', '));
  }
  if (sport && f.dist.length) {
    const b = m.buckets[f.sport] || [];
    out.push(f.dist.map((i) => (b[i] || {}).label || '').filter(Boolean).join(', '));
  }
  if (sport && f.type) {
    const t = (m.types[f.sport] || []).find((x) => x.code === f.type);
    if (t) out.push(t.label);
  }
  return out;
}

// filterTags — активные фильтры мелкими чипами.
export function filterTags(f, m) {
  return el('div', { class: 'tags' }, ...parts(f, m).map((text) => el('span', { class: 'tag', text })));
}

// chips — ряд переключателей; multi — несколько активных; у варианта может
// быть значок icon. round — круглые кнопки с подписью снизу (главная).
function chips(options, selected, multi, onChange, round) {
  const row = el('div', { class: round ? 'chips sports' : 'chips', role: 'group' });
  for (const o of options) {
    const pressed = multi ? selected.includes(o.value) : selected === o.value;
    row.append(el('button', {
      type: 'button', class: round ? 'sport' : 'chip', 'aria-pressed': String(pressed),
      onclick: () => {
        if (multi) onChange(pressed ? selected.filter((v) => v !== o.value) : [...selected, o.value]);
        else onChange(pressed ? '' : o.value);
      },
    }, o.icon, o.label));
  }
  return row;
}

// directions — направления: «Все» и виды спорта (как в меню бота: бег
// всегда, остальные — только с будущими стартами), ниже — чипы дисциплин
// выбранного вида. Выбор пишется в фильтры f; onChange — после записи;
// round — виды спорта круглыми кнопками (главная), иначе чипами.
export function directions(m, f, onChange, round) {
  const box = el('div', { class: 'field' });
  const set = (patch) => {
    Object.assign(f, patch);
    saveFilters(f);
    draw();
    onChange();
  };
  function draw() {
    const sports = m.sports.filter((s) => s.code === 'running' || s.count > 0);
    box.replaceChildren(chips([{ value: '', label: 'Все', icon: round ? glyph('grid') : null }, ...sports.map((s) => ({ value: s.code, label: s.title, icon: icon(s.code, '') }))],
      f.sport, false, (v) => set({ sport: v, dist: [], type: '' }), round));
    const types = (f.sport && m.types[f.sport]) || [];
    if (types.length) {
      box.append(chips(types.map((t) => ({ value: t.code, label: t.label, icon: icon(f.sport, t.code) })),
        f.type, false, (v) => set({ type: v })));
    }
  }
  draw();
  return box;
}

function field(title, ...children) {
  return el('div', { class: 'field' }, el('h3', { text: title }), ...children);
}

// debounce — вызов fn не чаще, чем раз в ms после последнего события.
function debounce(fn, ms) {
  let timer = 0;
  return (...args) => { clearTimeout(timer); timer = setTimeout(() => fn(...args), ms); };
}

export async function render(box) {
  box.replaceChildren();
  let m;
  try {
    m = await meta();
  } catch (e) {
    showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;
  const f = loadFilters();
  if (f.sport && !m.sports.some((s) => s.code === f.sport)) { f.sport = ''; f.dist = []; f.type = ''; }

  const update = (patch) => {
    Object.assign(f, patch);
    if (f.country === ALL_COUNTRIES) { f.city = ''; f.cityName = ''; }
    saveFilters(f);
    draw();
  };

  // Город: топ по текущему фильтру и подсказка по тексту (от 2 символов).
  let suggestSeq = 0;
  const cityBox = el('div', { class: 'field' });
  async function drawCity() {
    cityBox.replaceChildren(el('h3', { text: 'Город' }));
    if (f.country === ALL_COUNTRIES) {
      cityBox.append(el('p', { class: 'hint', text: 'Выберите страну, чтобы отфильтровать по городу.' }));
      return;
    }
    if (f.city) {
      cityBox.append(chips([{ value: f.city, label: (f.cityName || f.city) + ' ✕' }], f.city, false, () => update({ city: '', cityName: '' })));
    }
    const top = el('div', { class: 'chips' });
    const found = el('div', { class: 'chips' });
    const input = el('input', {
      type: 'search', placeholder: 'Другой город', autocomplete: 'off', maxlength: '64', 'aria-label': 'Поиск города',
      oninput: debounce(async () => {
        const q = input.value.trim();
        const my = ++suggestSeq;
        found.replaceChildren();
        if (q.length < 2) return;
        try {
          const list = await get('cities/suggest', { country: f.country, q });
          if (my !== suggestSeq || !cityBox.isConnected) return;
          if (!list.length) found.append(el('span', { class: 'hint', text: 'Ничего не найдено' }));
          found.append(chips(list.map((c) => ({ value: c.key, label: c.name })), f.city, false,
            (key) => update({ city: key, cityName: (list.find((c) => c.key === key) || {}).name || '' })));
        } catch (e) {
          if (my === suggestSeq && cityBox.isConnected) showError(found, e, null);
        }
      }, 300),
    });
    cityBox.append(top, input, found);
    try {
      const { city, ...q } = toQuery(f);
      const list = await get('cities', q);
      if (!cityBox.isConnected) return;
      top.append(chips(list.map((c) => ({ value: c.key, label: `${c.name} · ${c.count}` })), f.city, false,
        (key) => update({ city: key, cityName: (list.find((c) => c.key === key) || {}).name || '' })));
    } catch (e) {
      if (cityBox.isConnected) showError(top, e, drawCity);
    }
  }

  function draw() {
    box.replaceChildren();
    box.append(heading('Настрой поиск', 'Фильтры',
      el('button', { type: 'button', class: 'pill', text: 'Сбросить', onclick: () => { Object.assign(f, DEFAULTS, { dist: [], periods: [] }); saveFilters(f); draw(); } })));

    box.append(field('Вид спорта', chips([{ value: '', label: 'Все' }, ...m.sports.map((s) => ({ value: s.code, label: `${s.emoji} ${s.title}` }))],
      f.sport, false, (v) => update({ sport: v, dist: [], type: '' }))));

    const country = el('select', { 'aria-label': 'Страна', onchange: () => update({ country: country.value }) });
    for (const c of m.countries) {
      country.append(el('option', { value: c.code, selected: c.code === f.country, text: `${flag(c.code)} ${c.name} · ${c.count}` }));
    }
    country.append(el('option', { value: ALL_COUNTRIES, selected: f.country === ALL_COUNTRIES, text: '🌍 Все страны' }));
    box.append(field('Страна', country));

    box.append(cityBox);
    drawCity();

    box.append(field('Даты', chips(m.months.map((o) => ({ value: o.code, label: o.label })),
      f.periods, true, (v) => update({ periods: v }))));

    if (f.sport) {
      const buckets = m.buckets[f.sport] || [];
      if (buckets.length) {
        box.append(field('Дистанция', chips(buckets.map((b) => ({ value: b.i, label: b.label })), f.dist, true, (v) => update({ dist: v }))));
      }
      const types = m.types[f.sport] || [];
      if (types.length) {
        const type = el('select', { 'aria-label': 'Дисциплина', onchange: () => update({ type: type.value }) },
          el('option', { value: '', selected: !f.type, text: 'Любая' }));
        for (const t of types) type.append(el('option', { value: t.code, selected: t.code === f.type, text: t.label }));
        box.append(field('Дисциплина', type));
      }
    }

    box.append(el('a', { class: 'btn btn-primary btn-block', href: '#/catalog', text: 'Показать старты' }));
  }
  draw();
}
