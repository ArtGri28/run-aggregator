// Точка входа Mini App: инициализация Telegram.WebApp (ready, expand, тема,
// BackButton) и маршрутизация по hash: #/ #/catalog #/filters #/event/<id>
// #/favorites #/profile.
// Вне Telegram (пустой initData) — только заглушка (D-09).

import { el, stub } from './ui.js';
import * as home from './screens/home.js';
import * as catalog from './screens/catalog.js';
import * as filters from './screens/filters.js';
import * as event from './screens/event.js';
import * as favorites from './screens/favorites.js';
import * as profile from './screens/profile.js';

const tg = window.Telegram && window.Telegram.WebApp;
const root = document.getElementById('app');

const ROUTES = [
  [/^#?\/?$/, home],
  [/^#\/catalog$/, catalog],
  [/^#\/filters$/, filters],
  [/^#\/event\/(\d{1,18})$/, event],
  [/^#\/favorites$/, favorites],
  [/^#\/profile$/, profile],
];

// Переменные темы, которые при светлой схеме берутся из themeParams Telegram;
// при тёмной остаётся палитра приложения.
const THEME_VARS = [['--bg', 'bg_color'], ['--bg-2', 'secondary_bg_color'], ['--bg-3', 'section_bg_color'],
  ['--text', 'text_color'], ['--hint', 'hint_color'], ['--link', 'link_color'], ['--border', 'section_separator_color']];

function applyTheme() {
  const style = document.documentElement.style;
  const params = (tg.colorScheme === 'light' && tg.themeParams) || {};
  for (const [v, key] of THEME_VARS) {
    if (/^#[0-9a-f]{6}$/i.test(params[key] || '')) style.setProperty(v, params[key]);
    else style.removeProperty(v);
  }
  style.setProperty('color-scheme', tg.colorScheme === 'light' ? 'light' : 'dark');
  const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim();
  try {
    tg.setHeaderColor(bg);
    tg.setBackgroundColor(bg);
  } catch { /* старый клиент без этих методов */ }
}

let current = home;
// Список, из которого открыта карточка: туда ведёт «Назад» карточки.
let listHash = '#/catalog';

function show(screen, param) {
  current = screen;
  if (screen === catalog) listHash = '#/catalog';
  if (screen === favorites) listHash = '#/favorites';
  const box = el('section', { class: 'screen' });
  root.replaceChildren(box);
  window.scrollTo(0, 0);
  if (screen.back) tg.BackButton.show(); else tg.BackButton.hide();
  screen.render(box, param);
}

function route() {
  const hash = window.location.hash || '#/';
  for (const [re, screen] of ROUTES) {
    const m = hash.match(re);
    if (m) { show(screen, m[1]); return; }
  }
  // Неизвестный hash (в том числе параметры запуска Telegram) — на главную.
  window.location.replace('#/');
}

if (!tg || !tg.initData) {
  stub(root);
} else {
  tg.ready();
  tg.expand();
  applyTheme();
  tg.onEvent('themeChanged', applyTheme);
  tg.BackButton.onClick(() => { window.location.hash = current === event ? listHash : current.back || '#/'; });
  window.addEventListener('hashchange', route);
  route();
}
