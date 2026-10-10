// Экран фильтров и состояние фильтров каталога. Состояние живёт только на
// клиенте (D-10) под одним ключом localStorage; в базу не пишется. Это
// единственный модуль, который обращается к localStorage.

import { get, meta } from '../api.js';
import { el, flag, showError } from '../ui.js';

const KEY = 'gde-start.filters.v1';
const ALL_COUNTRIES = '*';
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
  f.periods = Array.isArray(f.periods) ? f.periods.map(String) : [];
  return f;
}

export function saveFilters(f) {
  try { localStorage.setItem(KEY, JSON.stringify(f)); } catch { /* хранилище недоступно — фильтры живут до перезагрузки */ }
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

const PERIOD_LABEL = { cm: 'этот месяц', nm: 'следующий месяц' };

// summary — строка активных фильтров для каталога по справочникам meta.
export function summary(f, m) {
  const parts = [];
  const sport = m.sports.find((s) => s.code === f.sport);
  if (sport) parts.push(sport.title);
  if (f.country === ALL_COUNTRIES) {
    parts.push('все страны');
  } else {
    const c = m.countries.find((x) => x.code === f.country);
    parts.push(c ? c.name : f.country);
    if (f.city) parts.push(f.cityName || f.city);
  }
  if (f.periods.length) {
    parts.push(f.periods.map((p) => PERIOD_LABEL[p] || (m.months.find((x) => x.code === p) || {}).label || p).join(', '));
  }
  if (sport && f.dist.length) {
    const b = m.buckets[f.sport] || [];
    parts.push(f.dist.map((i) => (b[i] || {}).label || '').filter(Boolean).join(', '));
  }
  if (sport && f.type) {
    const t = (m.types[f.sport] || []).find((x) => x.code === f.type);
    if (t) parts.push(t.label);
  }
  return parts.join(' · ');
}

// chips — ряд переключателей; multi — несколько активных.
function chips(options, selected, multi, onChange) {
  const row = el('div', { class: 'chips', role: 'group' });
  for (const o of options) {
    const pressed = multi ? selected.includes(o.value) : selected === o.value;
    row.append(el('button', {
      type: 'button', class: 'chip', 'aria-pressed': String(pressed), text: o.label,
      onclick: () => {
        if (multi) onChange(pressed ? selected.filter((v) => v !== o.value) : [...selected, o.value]);
        else onChange(pressed ? '' : o.value);
      },
    }));
  }
  return row;
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
    box.append(el('div', { class: 'bar' }, el('h2', { text: 'Фильтры' }),
      el('button', { type: 'button', class: 'btn', text: 'Сбросить', onclick: () => { Object.assign(f, DEFAULTS, { dist: [], periods: [] }); saveFilters(f); draw(); } })));

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

    box.append(field('Даты', chips([
      { value: 'cm', label: 'Этот месяц' }, { value: 'nm', label: 'Следующий месяц' },
      ...m.months.map((o) => ({ value: o.code, label: o.label })),
    ], f.periods, true, (v) => update({ periods: v }))));

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
