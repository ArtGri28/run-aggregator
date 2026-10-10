// Главная: бренд, виды спорта с числом будущих стартов (из meta), входы в
// каталог, фильтры, избранное и профиль. Календарь — заглушка до этапа 5.

import { meta } from '../api.js';
import { el, plural, showError } from '../ui.js';
import { plaque } from '../icons.js';
import { loadFilters, saveFilters } from './filters.js';

export const back = null;

function sportTile(s) {
  return el('button', {
    type: 'button', class: 'tile',
    onclick: () => {
      saveFilters({ ...loadFilters(), sport: s.code, dist: [], type: '' });
      window.location.hash = '#/catalog';
    },
  }, plaque(s.code, ''), el('span', {},
    el('div', { class: 'title', text: s.title }),
    el('div', { class: 'count', text: `${s.count} ${plural(s.count, 'старт', 'старта', 'стартов')}` })));
}

function soonTile(title) {
  return el('button', { type: 'button', class: 'tile', disabled: true },
    el('span', { class: 'title', text: title }), el('span', { class: 'soon', text: 'скоро' }));
}

function linkTile(title, href) {
  return el('a', { class: 'tile', href }, el('span', { class: 'title', text: title }));
}

export async function render(box) {
  box.replaceChildren(el('header', { class: 'brand' },
    el('h1', {}, 'Где ', el('span', { class: 'accent', text: 'старт?' })),
    el('p', { class: 'slogan', text: 'Все старты в одном месте' })));
  let m;
  try {
    m = await meta();
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;
  // Как в меню бота: бег показывается всегда, остальные виды — только с
  // будущими стартами.
  const sports = m.sports.filter((s) => s.code === 'running' || s.count > 0);
  box.append(el('h3', { text: 'Виды спорта' }), el('div', { class: 'tiles' }, ...sports.map(sportTile)));
  box.append(el('div', { class: 'btn-row' },
    el('a', { class: 'btn btn-primary', href: '#/catalog', text: 'Все старты' }),
    el('a', { class: 'btn', href: '#/filters', text: 'Фильтры' })));
  box.append(el('h3', { text: 'Разделы' }),
    el('div', { class: 'tiles' }, soonTile('Календарь'), linkTile('Избранное', '#/favorites'), linkTile('Профиль', '#/profile')));
}
