// Точка входа Mini App: инициализация Telegram.WebApp (ready, expand, цвета,
// BackButton), шапка с пилюлей страны, нижняя панель разделов и
// маршрутизация по hash: #/ #/catalog[?q=<запрос>] #/filters #/event/<id>
// #/calendar[/<месяц или день>] #/favorites #/profile[/calendar].
// Вне Telegram (пустой initData) — только заглушка (D-09).

import { meta } from './api.js';
import { el, flag, stub } from './ui.js';
import { glyph } from './icons.js';
import * as home from './screens/home.js';
import * as catalog from './screens/catalog.js';
import * as filters from './screens/filters.js';
import * as event from './screens/event.js';
import * as calendar from './screens/calendar.js';
import * as favorites from './screens/favorites.js';
import * as profile from './screens/profile.js';

const tg = window.Telegram && window.Telegram.WebApp;
const root = document.getElementById('app');

const ROUTES = [
  [/^#?\/?$/, home],
  [/^#\/catalog(?:\?q=([^&#]*))?$/, catalog],
  [/^#\/filters$/, filters],
  [/^#\/event\/(\d{1,18})$/, event],
  [/^#\/calendar(?:\/(\d{4}-\d{2}(?:-\d{2})?))?$/, calendar],
  [/^#\/favorites$/, favorites],
  [/^#\/profile(?:\/(calendar))?$/, profile],
];

// Вкладки нижней панели; фильтры и карточка подсвечивают «Каталог».
const TABS = [
  ['#/', 'Главная', 'home', home],
  ['#/catalog', 'Каталог', 'grid', catalog],
  ['#/calendar', 'Календарь', 'calendar', calendar],
  ['#/favorites', 'Избранное', 'heart', favorites],
  ['#/profile', 'Профиль', 'user', profile],
];

// match — экран и параметр маршрута по hash; неизвестный hash — null.
function match(hash) {
  for (const [re, screen] of ROUTES) {
    const m = (hash || '#/').match(re);
    if (m) return [screen, m[1]];
  }
  return null;
}

// Оформление всегда тёмное: палитра приложения не зависит от схемы Telegram.
function applyColors() {
  const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim();
  try {
    tg.setHeaderColor(bg);
    tg.setBackgroundColor(bg);
  } catch { /* старый клиент без этих методов */ }
}

// Пилюля страны — быстрый выбор страны фильтра: то же состояние, что на
// экране фильтров. Смена страны сбрасывает город и перерисовывает экран.
const country = el('select', {
  'aria-label': 'Страна',
  onchange: () => {
    filters.saveFilters({ ...filters.loadFilters(), country: country.value, city: '', cityName: '' });
    route();
  },
});
const countryPill = el('label', { class: 'pill-select', hidden: true }, country, glyph('chevron'));

async function syncCountry() {
  let m;
  try { m = await meta(); } catch { return; } // ошибку и повтор показывает экран
  const cur = filters.loadFilters().country;
  const opts = m.countries.map((c) => [c.code, `${flag(c.code)} ${c.name}`]);
  if (cur !== filters.ALL_COUNTRIES && !opts.some(([code]) => code === cur)) opts.unshift([cur, `${flag(cur)} ${cur}`]);
  opts.push([filters.ALL_COUNTRIES, 'Все страны']);
  country.replaceChildren(...opts.map(([value, text]) => el('option', { value, text })));
  country.value = cur;
  countryPill.hidden = false;
}

const tabs = TABS.map(([href, text, name, screen]) => [screen, el('a', { href }, glyph(name), el('span', { text }))]);

function shell() {
  document.getElementById('top').append(
    el('a', { class: 'brand', href: '#/' },
      el('span', { class: 'logo' }, glyph('pin')),
      el('span', { class: 'words' }, el('b', { text: 'Где старт?' }), el('span', { class: 'slogan', text: 'Все старты в одном месте.' }))),
    countryPill);
  document.getElementById('nav').append(...tabs.map(([, a]) => a));
  window.addEventListener('filters', syncCountry);
}

// Список, из которого открыта карточка: туда ведёт «Назад» карточки.
let listHash = '#/catalog';

function show(screen, param) {
  const box = el('section', { class: 'screen' });
  root.replaceChildren(box);
  window.scrollTo(0, 0);
  if (screen.back) tg.BackButton.show(); else tg.BackButton.hide();
  const tab = screen === filters || screen === event ? catalog : screen;
  for (const [s, a] of tabs) {
    if (s === tab) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  }
  syncCountry();
  screen.render(box, param);
}

function route(e) {
  const found = match(window.location.hash);
  if (!found) {
    // Неизвестный hash (в том числе параметры запуска Telegram) — на главную.
    window.location.replace('#/');
    return;
  }
  // Карточка запоминает экран, с которого открыта (главная, каталог,
  // календарь с месяцем или днём, избранное).
  if (found[0] === event && e && e.oldURL) {
    const from = new URL(e.oldURL).hash || '#/';
    const prev = match(from);
    if (prev && prev[0] !== event && prev[0] !== filters) listHash = from;
  }
  show(...found);
}

if (!tg || !tg.initData) {
  stub(root);
} else {
  tg.ready();
  tg.expand();
  applyColors();
  shell();
  // back экрана — hash или функция от параметра маршрута (календарь);
  // маршрут читается из текущего hash: календарь меняет его без перехода.
  tg.BackButton.onClick(() => {
    const [screen, param] = match(window.location.hash) || [home];
    const back = screen === event ? listHash : screen.back;
    window.location.hash = (typeof back === 'function' ? back(param) : back) || '#/';
  });
  window.addEventListener('hashchange', route);
  route();
}
