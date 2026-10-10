// Профиль: число избранного, ответы анкеты GET /profile только для чтения и
// текущие фильтры каталога. Анкета заполняется и меняется в боте; кнопка
// закрывает Mini App и возвращает в чат.

import { get, meta } from '../api.js';
import { el, heading, showError } from '../ui.js';
import { glyph } from '../icons.js';
import { filterTags, loadFilters } from './filters.js';

export const back = '#/';

function tile(value, label, href) {
  const cls = typeof value === 'number' ? 'num' : 'val';
  return el(href ? 'a' : 'div', { class: 'tile', href }, el('span', { class: cls, text: String(value) }), el('span', { class: 'sub', text: label }));
}

function toBot(text) {
  return el('button', { type: 'button', class: 'btn btn-block', text, onclick: () => window.Telegram.WebApp.close() });
}

export async function render(box) {
  box.replaceChildren(heading('Твой спортивный сезон', 'Профиль'));
  let p;
  let fav;
  let m;
  try {
    [p, fav, m] = await Promise.all([get('profile'), get('favorites', { limit: 1 }), meta()]);
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;

  const who = el('div', { class: 'card who' }, el('span', { class: 'plaque plaque-lg plaque-accent' }, glyph('user')));
  const tiles = el('div', { class: 'tiles' }, tile(fav.total, 'В избранном', '#/favorites'));
  box.append(who, tiles);
  if (p.exists) {
    const label = (o, empty) => (o ? o.label : empty);
    who.append(el('p', { class: 'hint', text: 'Ответы анкеты меняются в боте, в разделе «Мой профиль».' }));
    tiles.append(
      tile(label(p.gender, 'не указан'), 'Пол'),
      tile(label(p.age_group, 'не указан'), 'Возраст'),
      tile(label(p.country, 'не указана'), 'Страна'),
      tile(p.city || 'не указан', 'Город'),
      tile(p.sports.length ? p.sports.map((s) => s.label).join(', ') : 'не указан', 'Спорт'));
    box.append(toBot('Изменить в боте'));
  } else {
    who.append(el('p', { text: 'Анкета заполняется в боте.' }),
      el('p', { class: 'hint', text: 'Вернитесь в чат с ботом и отправьте команду /start.' }));
    box.append(toBot('Вернуться в бот'));
  }

  box.append(el('h2', { text: 'Предпочтения' }),
    el('div', { class: 'card' },
      el('p', { class: 'hint', text: 'Фильтры каталога' }),
      filterTags(loadFilters(), m),
      el('a', { class: 'btn btn-primary btn-block', href: '#/filters', text: 'Настроить фильтры' })));
}
